// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package embeddocs implements embedding text docs into a vector database.
package embeddocs

import (
	"log/slog"

	"rsc.io/gaby/internal/docs"
	"rsc.io/gaby/internal/llm"
	"rsc.io/gaby/internal/storage"
	"rsc.io/gaby/internal/storage/timed"
)

// Sync reads new documents from dc, embeds them using embed,
// and then writes the (docid, vector) pairs to vdb.
//
// Sync uses [docs.DocWatcher] with the the name “embeddocs” to
// save its position across multiple calls.
//
// Sync logs status and unexpected problems to lg.
func Sync(lg *slog.Logger, vdb storage.VectorDB, embed llm.Embedder, dc *docs.Corpus) {
	lg.Info("embeddocs sync")

	const batchSize = 100
	var (
		batch     []llm.EmbedDoc
		ids       []string
		batchLast timed.DBTime
	)
	w := dc.DocWatcher("embeddocs")

	flush := func() bool {
		vecs, err := embed.EmbedDocs(batch)
		if len(vecs) > len(ids) {
			lg.Error("embeddocs length mismatch", "batch", len(batch), "vecs", len(vecs), "ids", len(ids))
			return false
		}
		for i, v := range vecs {
			vdb.Set(ids[i], v)
		}
		if err != nil {
			lg.Error("embeddocs EmbedDocs error", "err", err)
			return false
		}
		if len(vecs) != len(ids) {
			lg.Error("embeddocs length mismatch", "batch", len(batch), "vecs", len(vecs), "ids", len(ids))
			return false
		}
		vdb.Flush() // todo vdb
		w.MarkOld(batchLast)
		w.Flush()
		batch = nil
		ids = nil
		return true
	}

	for d := range w.Recent() {
		lg.Debug("embeddocs sync start", "doc", d.ID)
		batch = append(batch, llm.EmbedDoc{Title: d.Title, Text: d.Text})
		ids = append(ids, d.ID)
		batchLast = d.DBTime
		if len(batch) >= batchSize {
			if !flush() {
				break
			}
		}
	}
	if len(batch) > 0 {
		// More to flush, but flush uses w.MarkOld,
		// which has to be called during an iteration over w.Recent.
		// Start a new iteration just to call flush and then break out.
		for _ = range w.Recent() {
			if !flush() {
				return
			}
			break
		}
	}
}
