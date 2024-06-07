// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gemini

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"rsc.io/gaby/internal/httprr"
	"rsc.io/gaby/internal/llm"
	"rsc.io/gaby/internal/secret"
	"rsc.io/gaby/internal/testutil"
)

var docs = []llm.EmbedDoc{
	{Text: "for loops"},
	{Text: "for all time, always"},
	{Text: "break statements"},
	{Text: "breakdancing"},
	{Text: "forever could never be long enough for me"},
	{Text: "the macarena"},
}

var matches = map[string]string{
	"for loops":            "break statements",
	"for all time, always": "forever could never be long enough for me",
	"breakdancing":         "the macarena",
}

func init() {
	for k, v := range matches {
		matches[v] = k
	}
}

func newTestClient(t *testing.T, rrfile string) *Client {
	check := testutil.Checker(t)
	lg := testutil.Slogger(t)

	rr, err := httprr.Open(rrfile, http.DefaultTransport)
	check(err)
	rr.Scrub(Scrub)
	sdb := secret.Netrc()

	c, err := NewClient(lg, sdb, rr.Client())
	check(err)

	return c
}

func TestEmbedBatch(t *testing.T) {
	check := testutil.Checker(t)
	c := newTestClient(t, "testdata/embedbatch.httprr")
	vecs, err := c.EmbedDocs(docs)
	check(err)
	if len(vecs) != len(docs) {
		t.Fatalf("len(vecs) = %d, but len(docs) = %d", len(vecs), len(docs))
	}

	var buf bytes.Buffer
	for i := range docs {
		for j := range docs {
			fmt.Fprintf(&buf, " %.4f", vecs[i].Dot(vecs[j]))
		}
		fmt.Fprintf(&buf, "\n")
	}

	for i, d := range docs {
		best := ""
		bestDot := 0.0
		for j := range docs {
			if dot := vecs[i].Dot(vecs[j]); i != j && dot > bestDot {
				best, bestDot = docs[j].Text, dot
			}
		}
		if best != matches[d.Text] {
			if buf.Len() > 0 {
				t.Errorf("dot matrix:\n%s", buf.String())
				buf.Reset()
			}
			t.Errorf("%q: best=%q, want %q", d.Text, best, matches[d.Text])
		}
	}
}

func TestBigBatch(t *testing.T) {
	check := testutil.Checker(t)
	c := newTestClient(t, "testdata/bigbatch.httprr")
	var docs []llm.EmbedDoc
	data, err := os.ReadFile("/usr/local/plan9/lib/words")
	check(err)
	for _, w := range strings.Fields(string(data)) {
		docs = append(docs, llm.EmbedDoc{Text: w})
	}
	docs = docs[:251]
	vecs, err := c.EmbedDocs(docs)
	check(err)
	if len(vecs) != len(docs) {
		t.Fatalf("len(vecs) = %d, but len(docs) = %d", len(vecs), len(docs))
	}
}
