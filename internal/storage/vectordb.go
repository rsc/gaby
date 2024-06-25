// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"cmp"

	"rsc.io/gaby/internal/llm"
)

// A VectorDB is a vector database that implements
// nearest-neighbor search over embedding vectors
// corresponding to documents.
type VectorDB interface {
	// Set sets the vector associated with the given document ID to vec.
	Set(id string, vec llm.Vector)

	// TODO: Add Delete.

	// Get gets the vector associated with the given document ID.
	// If no such document exists, Get returns nil, false.
	// If a document exists, Get returns vec, true.
	Get(id string) (llm.Vector, bool)

	// Batch returns a new [VectorBatch] that accumulates
	// vector database mutations to apply in an atomic operation.
	// It is more efficient than repeated calls to Set.
	Batch() VectorBatch

	// Search searches the database for the n vectors
	// most similar to vec, returning the document IDs
	// and similarity scores.
	Search(vec llm.Vector, n int) []VectorResult

	// Flush flushes storage to disk.
	Flush()
}

// A VectorBatch accumulates vector database mutations
// that are applied to a [VectorDB] in a single atomic operation.
// Applying bulk operations in a batch is also more efficient than
// making individual [VectorDB] method calls.
// The batched operations apply in the order they are made.
type VectorBatch interface {
	// Set sets the vector associated with the given document ID to vec.
	Set(id string, vec llm.Vector)

	// TODO: Add Delete.

	// MaybeApply calls Apply if the VectorBatch is getting close to full.
	// Every VectorBatch has a limit to how many operations can be batched,
	// so in a bulk operation where atomicity of the entire batch is not a concern,
	// calling MaybeApply gives the VectorBatch implementation
	// permission to flush the batch at specific “safe points”.
	// A typical limit for a batch is about 100MB worth of logged operations.
	//
	// MaybeApply reports whether it called Apply.
	MaybeApply() bool

	// Apply applies all the batched operations to the underlying VectorDB
	// as a single atomic unit.
	// When Apply returns, the VectorBatch is an empty batch ready for
	// more operations.
	Apply()
}

// A VectorResult is a single document returned by a VectorDB search.
type VectorResult struct {
	ID    string  // document ID
	Score float64 // similarity score in range [0, 1]; 1 is exact match
}

func (x VectorResult) cmp(y VectorResult) int {
	if x.Score != y.Score {
		return cmp.Compare(x.Score, y.Score)
	}
	return cmp.Compare(x.ID, y.ID)
}
