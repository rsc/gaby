// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package llm

import (
	"bytes"
	"fmt"
	"testing"
)

func TestQuote(t *testing.T) {
	docs := []EmbedDoc{{Text: "abc"}, {Text: "alphabetical order"}}
	vecs, err := QuoteEmbedder().EmbedDocs(docs)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != len(docs) {
		t.Fatalf("len(docs) = %v, but len(vecs) = %d", len(docs), len(vecs))
	}
	for i, v := range vecs {
		u := UnquoteVector(v)
		if u != docs[i].Text {
			var buf bytes.Buffer
			for i, f := range v {
				fmt.Fprintf(&buf, " %f", f)
				if f < 0 {
					if i < len(v)-1 {
						fmt.Fprintf(&buf, " ... %f", v[len(v)-1])
					}
					break
				}
			}
			t.Logf("Embed(%q) = %v", docs[i].Text, buf.String())
			t.Errorf("Unquote() = %q, want %q", u, docs[i].Text)
		}
	}
}
