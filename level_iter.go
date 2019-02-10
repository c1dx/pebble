// Copyright 2018 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package pebble

import (
	"sort"

	"github.com/petermattis/pebble/db"
)

// tableNewIters creates a new point and range-del iterator for the given file
// number.
type tableNewIters func(
	meta *fileMetadata, opts *db.IterOptions,
) (internalIterator, internalIterator, error)

// levelIter provides a merged view of the sstables in a level.
//
// levelIter is used during compaction and as part of the Iterator
// implementation. When used as part of the Iterator implementation, level
// iteration needs to "pause" at sstable boundaries if a range deletion
// tombstone is the source of that boundary. We know if a range tombstone is
// the smallest or largest key in a file because the kind will be
// InternalKeyKindRangeDeletion. If the boundary key is a range deletion
// tombstone, we materialize a fake entry to return from levelIter. This
// prevents mergingIter from advancing past the sstable until the sstable
// contains the smallest (or largest for reverse iteration) key in the merged
// heap. Note that Iterator treat a range deletion tombstone as a no-op and
// processes range deletions via mergingIter.
type levelIter struct {
	opts  *db.IterOptions
	cmp   db.Compare
	index int
	// The key to return when iterating past an sstable boundary and that
	// boundary is a range deletion tombstone. Note that if boundary != nil, then
	// iter == nil, and if iter != nil, then boundary == nil.
	boundary     *db.InternalKey
	iter         internalIterator
	newIters     tableNewIters
	rangeDelIter *internalIterator
	files        []fileMetadata
	err          error
}

// levelIter implements the internalIterator interface.
var _ internalIterator = (*levelIter)(nil)

func newLevelIter(
	opts *db.IterOptions, cmp db.Compare, newIters tableNewIters, files []fileMetadata,
) *levelIter {
	l := &levelIter{}
	l.init(opts, cmp, newIters, files)
	return l
}

func (l *levelIter) init(
	opts *db.IterOptions, cmp db.Compare, newIters tableNewIters, files []fileMetadata,
) {
	l.opts = opts
	l.cmp = cmp
	l.index = -1
	l.newIters = newIters
	l.files = files
}

func (l *levelIter) initRangeDel(rangeDelIter *internalIterator) {
	l.rangeDelIter = rangeDelIter
}

func (l *levelIter) findFileGE(key []byte) int {
	// Find the earliest file whose largest key is >= ikey. Note that the range
	// deletion sentinel key is handled specially and a search for K will not
	// find a table where K<range-del-sentinel> is the largest key. This prevents
	// loading untruncated range deletions from a table which can't possibly
	// contain the target key and is required for correctness by DB.Get.
	//
	// TODO(peter): inline the binary search.
	return sort.Search(len(l.files), func(i int) bool {
		largest := &l.files[i].largest
		c := l.cmp(largest.UserKey, key)
		if c > 0 {
			return true
		}
		return c == 0 && largest.Trailer != db.InternalKeyRangeDeleteSentinel
	})
}

func (l *levelIter) findFileLT(key []byte) int {
	// Find the last file whose smallest key is < ikey.
	index := sort.Search(len(l.files), func(i int) bool {
		return l.cmp(l.files[i].smallest.UserKey, key) >= 0
	})
	return index - 1
}

func (l *levelIter) loadFile(index, dir int) bool {
	l.boundary = nil
	if l.index == index {
		return l.iter != nil
	}
	if l.iter != nil {
		l.err = l.iter.Close()
		if l.err != nil {
			return false
		}
		l.iter = nil
	}
	if l.rangeDelIter != nil {
		*l.rangeDelIter = nil
	}

	for ; ; index += dir {
		l.index = index
		if l.index < 0 || l.index >= len(l.files) {
			return false
		}

		f := &l.files[l.index]
		if lowerBound := l.opts.GetLowerBound(); lowerBound != nil {
			if l.cmp(f.largest.UserKey, lowerBound) < 0 {
				// The largest key in the sstable is smaller than the lower bound.
				if dir < 0 {
					return false
				}
				continue
			}
		}
		if upperBound := l.opts.GetUpperBound(); upperBound != nil {
			if l.cmp(f.smallest.UserKey, upperBound) >= 0 {
				// The smallest key in the sstable is greater than or equal to the
				// lower bound.
				if dir > 0 {
					return false
				}
				continue
			}
		}

		var rangeDelIter internalIterator
		l.iter, rangeDelIter, l.err = l.newIters(f, l.opts)
		if l.err != nil || l.iter == nil {
			return false
		}
		if l.rangeDelIter != nil {
			*l.rangeDelIter = rangeDelIter
		}
		return true
	}
}

func (l *levelIter) SeekGE(key []byte) bool {
	// NB: the top-level Iterator has already adjusted key based on
	// IterOptions.LowerBound.
	if !l.loadFile(l.findFileGE(key), 1) {
		return false
	}
	if l.iter.SeekGE(key) {
		return true
	}
	return l.skipEmptyFileForward()
}

func (l *levelIter) SeekLT(key []byte) bool {
	// NB: the top-level Iterator has already adjusted key based on
	// IterOptions.UpperBound.
	if !l.loadFile(l.findFileLT(key), -1) {
		return false
	}
	if l.iter.SeekLT(key) {
		return true
	}
	return l.skipEmptyFileBackward()
}

func (l *levelIter) First() bool {
	// NB: the top-level Iterator will call SeekGE if IterOptions.LowerBound is
	// set.
	if !l.loadFile(0, 1) {
		return false
	}
	if l.iter.First() {
		return true
	}
	return l.skipEmptyFileForward()
}

func (l *levelIter) Last() bool {
	// NB: the top-level Iterator will call SeekLT if IterOptions.UpperBound is
	// set.
	if !l.loadFile(len(l.files)-1, -1) {
		return false
	}
	if l.iter.Last() {
		return true
	}
	return l.skipEmptyFileBackward()
}

func (l *levelIter) Next() bool {
	if l.err != nil {
		return false
	}

	if l.iter == nil {
		if l.boundary != nil {
			if l.loadFile(l.index+1, 1) {
				if l.iter.First() {
					return true
				}
				return l.skipEmptyFileForward()
			}
			return false
		}
		if l.index == -1 && l.loadFile(0, 1) {
			// The iterator was positioned off the beginning of the level. Position
			// at the first entry.
			if l.iter.First() {
				return true
			}
			return l.skipEmptyFileForward()
		}
		return false
	}

	if l.iter.Next() {
		return true
	}
	return l.skipEmptyFileForward()
}

func (l *levelIter) Prev() bool {
	if l.err != nil {
		return false
	}

	if l.iter == nil {
		if l.boundary != nil {
			if l.loadFile(l.index-1, -1) {
				if l.iter.Last() {
					return true
				}
				return l.skipEmptyFileBackward()
			}
			return false
		}
		if n := len(l.files); l.index == n && l.loadFile(n-1, -1) {
			// The iterator was positioned off the end of the level. Position at the
			// last entry.
			if l.iter.Last() {
				return true
			}
			return l.skipEmptyFileBackward()
		}
		return false
	}

	if l.iter.Prev() {
		return true
	}
	return l.skipEmptyFileBackward()
}

func (l *levelIter) skipEmptyFileForward() bool {
	for valid := false; !valid; valid = l.iter.First() {
		if l.err = l.iter.Close(); l.err != nil {
			return false
		}
		l.iter = nil

		if l.rangeDelIter != nil {
			// We're being used as part of an Iterator and we've reached the end of
			// the sstable. If the boundary is a range deletion tombstone, return
			// that key.
			if f := &l.files[l.index]; f.largest.Kind() == db.InternalKeyKindRangeDelete {
				l.boundary = &f.largest
				return true
			}
			*l.rangeDelIter = nil
		}

		// Current file was exhausted. Move to the next file.
		if !l.loadFile(l.index+1, 1) {
			return false
		}
	}
	return true
}

func (l *levelIter) skipEmptyFileBackward() bool {
	for valid := false; !valid; valid = l.iter.Last() {
		if l.err = l.iter.Close(); l.err != nil {
			return false
		}
		l.iter = nil

		if l.rangeDelIter != nil {
			// We're being used as part of an Iterator and we've reached the end of
			// the sstable. If the boundary is a range deletion tombstone, return
			// that key.
			if f := &l.files[l.index]; f.smallest.Kind() == db.InternalKeyKindRangeDelete {
				l.boundary = &f.smallest
				return true
			}
			*l.rangeDelIter = nil
		}

		// Current file was exhausted. Move to the previous file.
		if !l.loadFile(l.index-1, -1) {
			return false
		}
	}
	return true
}

func (l *levelIter) Key() db.InternalKey {
	if l.iter == nil {
		if l.boundary != nil {
			return *l.boundary
		}
		return db.InvalidInternalKey
	}
	return l.iter.Key()
}

func (l *levelIter) Value() []byte {
	if l.iter == nil {
		return nil
	}
	return l.iter.Value()
}

func (l *levelIter) Valid() bool {
	if l.iter == nil {
		return l.boundary != nil
	}
	return l.iter.Valid()
}

func (l *levelIter) Error() error {
	if l.err != nil || l.iter == nil {
		return l.err
	}
	return l.iter.Error()
}

func (l *levelIter) Close() error {
	if l.iter != nil {
		l.err = l.iter.Close()
		l.iter = nil
	}
	if l.rangeDelIter != nil {
		if t := *l.rangeDelIter; t != nil {
			if err := t.Close(); err != nil && l.err == nil {
				l.err = err
			}
		}
		*l.rangeDelIter = nil
	}
	return l.err
}
