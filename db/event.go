// Copyright 2018 The LevelDB-Go and Pebble Authors. All rights reserved. Use
// of this source code is governed by a BSD-style license that can be found in
// the LICENSE file.

package db

// TableInfo contains the common information for table related events.
type TableInfo struct {
	// Path is the location of the file on disk.
	Path string
	// FileNum is the internal DB identifier for the table.
	FileNum uint64
	// Size is the size of the file in bytes.
	Size uint64
	// Smallest is the smallest internal key in the table.
	Smallest InternalKey
	// Largest is the largest internal key in the table.
	Largest InternalKey
	// SmallestSeqNum is the smallest sequence number in the table.
	SmallestSeqNum uint64
	// LargestSeqNum is the largest sequence number in the table.
	LargestSeqNum uint64
}

// CompactionInfo contains the info for a compaction event.
type CompactionInfo struct {
	// JobID is the ID of the compaction job.
	JobID int
	// Reason is the reason for the compaction.
	Reason string
	// Input contains the input tables for the compaction. A compaction is
	// performed from Input.Level to Input.Level+1. Input.Tables[0] contains the
	// inputs from Input.Level and Input.Tables[1] contains the inputs from
	// Input.Level+1.
	Input struct {
		Level  int
		Tables [2][]TableInfo
	}
	// Output contains the output tables generated by the compaction. The output
	// tables are empty for the compaction begin event.
	Output struct {
		Level  int
		Tables []TableInfo
	}
	Err error
}

// FlushInfo contains the info for a flush event.
type FlushInfo struct {
	// JobID is the ID of the flush job.
	JobID int
	// Reason is the reason for the flush.
	Reason string
	// Output contains the ouptut table generated by the flush. The output info
	// is empty for the flush begin event.
	Output TableInfo
	Err    error
}

// TableDeleteInfo contains the info for a table deletion event.
type TableDeleteInfo struct {
	JobID   int
	Path    string
	FileNum uint64
	Err     error
}

// TableIngestInfo contains the info for a table ingestion event.
type TableIngestInfo struct {
	// JobID is the ID of the job the caused the table to be ingested.
	JobID  int
	Tables []struct {
		TableInfo
		Level int
	}
	// GlobalSeqNum is the sequence number that was assigned to all entries in
	// the ingested table.
	GlobalSeqNum uint64
	Err          error
}

// EventListener contains a set of functions that will be invoked when various
// significant DB events occur. Note that the functions should not run for an
// excessive amount of time as they are invokved synchronously by the DB and
// may block continued DB work. For a similar reason it is advisable to not
// perform any synchronous calls back into the DB.
type EventListener struct {
	// BackgroundError is invoked whenever an error occurs during a background
	// operation such as flush or compaction.
	BackgroundError func(error)

	// CompactionBegin is invoked after the inputs to a compaction have been
	// determined, but before the compaction has produced any output.
	CompactionBegin func(CompactionInfo)

	// CompactionEnd is invoked after a compaction has completed and the result
	// has been installed.
	CompactionEnd func(CompactionInfo)

	// FlushBegin is invoked after the inputs to a flush have been determined,
	// but before the flush has produced any output.
	FlushBegin func(FlushInfo)

	// FlushEnd is invoked after a flush has complated and the result has been
	// installed.
	FlushEnd func(FlushInfo)

	// TableDeleted is invoked after a table has been deleted.
	TableDeleted func(TableDeleteInfo)

	// TableIngested is invoked after an externally created table has been
	// ingested via a call to DB.Ingest().
	TableIngested func(TableIngestInfo)
}