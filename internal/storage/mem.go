// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package storage

import (
	"bytes"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"sync"

	"rsc.io/gaby/internal/llm"
	"rsc.io/omap"
	"rsc.io/ordered"
	"rsc.io/top"
)

// A MemLocker is an single-process implementation
// of the database Lock and Unlock methods,
// suitable if there is only one process accessing the
// database at a time.
type MemLocker struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Lock locks the mutex with the given name.
func (l *MemLocker) Lock(name string) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*sync.Mutex)
	}
	mu := l.locks[name]
	if mu == nil {
		mu = new(sync.Mutex)
		l.locks[name] = mu
	}
	l.mu.Unlock()

	mu.Lock()
}

// Unlock locks the mutex with the given name.
func (l *MemLocker) Unlock(name string) {
	l.mu.Lock()
	mu := l.locks[name]
	l.mu.Unlock()
	if mu == nil {
		panic("Unlock of never locked key")
	}
	mu.Unlock()
}

// MemDB returns an in-memory DB implementation.
func MemDB() DB {
	return new(memDB)
}

// A memDB is an in-memory DB implementation,.
type memDB struct {
	MemLocker
	mu   sync.RWMutex
	data omap.Map[string, []byte]
}

func (*memDB) Close() {}

func (*memDB) Panic(msg string, args ...any) {
	Panic(msg, args...)
}

// Get returns the value associated with the key.
func (db *memDB) Get(key []byte) (val []byte, ok bool) {
	db.mu.RLock()
	v, ok := db.data.Get(string(key))
	db.mu.RUnlock()
	if ok {
		v = bytes.Clone(v)
	}
	return v, ok
}

// Scan returns an iterator overall key-value pairs
// in the range start ≤ key ≤ end.
func (db *memDB) Scan(start, end []byte) iter.Seq2[[]byte, func() []byte] {
	lo := string(start)
	hi := string(end)
	return func(yield func(key []byte, val func() []byte) bool) {
		db.mu.RLock()
		locked := true
		defer func() {
			if locked {
				db.mu.RUnlock()
			}
		}()
		for k, v := range db.data.Scan(lo, hi) {
			key := []byte(k)
			val := func() []byte { return bytes.Clone(v) }
			db.mu.RUnlock()
			locked = false
			if !yield(key, val) {
				return
			}
			db.mu.RLock()
			locked = true
		}
	}
}

// Delete deletes any entry with the given key.
func (db *memDB) Delete(key []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.data.Delete(string(key))
}

// DeleteRange deletes all entries with start ≤ key ≤ end.
func (db *memDB) DeleteRange(start, end []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.data.DeleteRange(string(start), string(end))
}

// Set sets the value associated with key to val.
func (db *memDB) Set(key, val []byte) {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.data.Set(string(key), bytes.Clone(val))
}

// Batch returns a new batch.
func (db *memDB) Batch() Batch {
	return &memBatch{db: db}
}

// Flush flushes everything to persistent storage.
// Since this is an in-memory database, the memory is as persistent as it gets.
func (db *memDB) Flush() {
}

// A memBatch is a Batch for a memDB.
type memBatch struct {
	db  *memDB   // underlying database
	ops []func() // operations to apply
}

func (b *memBatch) Set(key, val []byte) {
	k := string(key)
	v := bytes.Clone(val)
	b.ops = append(b.ops, func() { b.db.data.Set(k, v) })
}

func (b *memBatch) Delete(key []byte) {
	k := string(key)
	b.ops = append(b.ops, func() { b.db.data.Delete(k) })
}

func (b *memBatch) DeleteRange(start, end []byte) {
	s := string(start)
	e := string(end)
	b.ops = append(b.ops, func() { b.db.data.DeleteRange(s, e) })
}

func (b *memBatch) MaybeApply() bool {
	return false
}

func (b *memBatch) Apply() {
	b.db.mu.Lock()
	defer b.db.mu.Unlock()

	for _, op := range b.ops {
		op()
	}
}

// A memVectorDB is a VectorDB implementing in-memory search
// but storing its vectors in an underlying DB.
type memVectorDB struct {
	storage   DB
	slog      *slog.Logger
	namespace string

	mu    sync.RWMutex
	cache map[string][]float32 // in-memory cache of all vectors, indexed by id
}

// MemVectorDB returns a VectorDB that stores its vectors in db
// but uses a cached, in-memory copy to implement Search using
// a brute-force scan.
//
// The namespace is incorporated into the keys used in the underlying db,
// to allow multiple vector databases to be stored in a single [DB].
//
// When MemVectorDB is called, it reads all previously stored vectors
// from db; after that, changes must be made using the MemVectorDB
// Set method.
//
// A MemVectorDB requires approximately 3kB of memory per stored vector.
//
// The db keys used by a MemVectorDB have the form
//
//	ordered.Encode("llm.Vector", namespace, id)
//
// where id is the document ID passed to Set.
func MemVectorDB(db DB, lg *slog.Logger, namespace string) VectorDB {
	// NOTE: The worst case score error in a dot product over 768 entries
	// caused by quantization error of e is approximately 54e,
	// so quantizing to int16s would only introduce a maximum score
	// error of 0.00165, which would not change results significantly.
	// So we could cut the memory per stored vector in half by
	// quantizing to int16.

	vdb := &memVectorDB{
		storage:   db,
		slog:      lg,
		namespace: namespace,
		cache:     make(map[string][]float32),
	}

	// Load all the previously-stored vectors.
	vdb.cache = make(map[string][]float32)
	for key, getVal := range vdb.storage.Scan(
		ordered.Encode("llm.Vector", namespace),
		ordered.Encode("llm.Vector", namespace, ordered.Inf)) {

		var id string
		if err := ordered.Decode(key, nil, nil, &id); err != nil {
			// unreachable except data corruption
			panic(fmt.Errorf("MemVectorDB decode key=%v: %v", Fmt(key), err))
		}
		val := getVal()
		if len(val)%4 != 0 {
			// unreachable except data corruption
			panic(fmt.Errorf("MemVectorDB decode key=%v bad len(val)=%d", Fmt(key), len(val)))
		}
		var vec llm.Vector
		vec.Decode(val)
		vdb.cache[id] = vec
	}

	vdb.slog.Info("loaded vectordb", "n", len(vdb.cache), "namespace", namespace)
	return vdb
}

func (db *memVectorDB) Set(id string, vec llm.Vector) {
	db.storage.Set(ordered.Encode("llm.Vector", db.namespace, id), vec.Encode())

	db.mu.Lock()
	db.cache[id] = slices.Clone(vec)
	db.mu.Unlock()
}

func (db *memVectorDB) Get(name string) (llm.Vector, bool) {
	db.mu.RLock()
	vec, ok := db.cache[name]
	db.mu.RUnlock()
	return vec, ok
}

func (db *memVectorDB) Search(target llm.Vector, n int) []VectorResult {
	db.mu.RLock()
	defer db.mu.RUnlock()
	best := top.New(n, VectorResult.cmp)
	for name, vec := range db.cache {
		if len(vec) != len(target) {
			continue
		}
		best.Add(VectorResult{name, target.Dot(vec)})
	}
	return best.Take()
}

func (db *memVectorDB) Flush() {
	db.storage.Flush()
}

// memVectorBatch implements VectorBatch for a memVectorDB.
type memVectorBatch struct {
	db *memVectorDB          // underlying memVectorDB
	sb Batch                 // batch for underlying DB
	w  map[string]llm.Vector // vectors to write
}

func (db *memVectorDB) Batch() VectorBatch {
	return &memVectorBatch{db, db.storage.Batch(), make(map[string]llm.Vector)}
}

func (b *memVectorBatch) Set(name string, vec llm.Vector) {
	b.sb.Set(ordered.Encode("llm.Vector", b.db.namespace, name), vec.Encode())

	b.w[name] = slices.Clone(vec)
}

func (b *memVectorBatch) MaybeApply() bool {
	if !b.sb.MaybeApply() {
		return false
	}
	b.Apply()
	return true
}

func (b *memVectorBatch) Apply() {
	b.sb.Apply()

	b.db.mu.Lock()
	defer b.db.mu.Unlock()

	for name, vec := range b.w {
		b.db.cache[name] = vec
	}
	clear(b.w)
}
