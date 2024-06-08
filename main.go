//go:build ignore

package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"rsc.io/gaby/internal/commentfix"
	"rsc.io/gaby/internal/docs"
	"rsc.io/gaby/internal/embeddocs"
	"rsc.io/gaby/internal/gemini"
	"rsc.io/gaby/internal/github"
	"rsc.io/gaby/internal/githubdocs"
	"rsc.io/gaby/internal/llm"
	"rsc.io/gaby/internal/pebble"
	"rsc.io/gaby/internal/related"
	"rsc.io/gaby/internal/secret"
	"rsc.io/gaby/internal/storage"
)

var searchMode = flag.Bool("search", false, "run in interactive search mode")

func main() {
	flag.Parse()
	// TODO gabysitter flag?

	lg := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	sdb := secret.Netrc()

	db, err := pebble.Open(lg, "gaby.db")
	if err != nil {
		log.Fatal(err)
	}

	vdb := storage.MemVectorDB(db, lg, "")

	gh := github.New(lg, db, secret.Netrc(), http.DefaultClient)
	/*
		gc.Add("rsc/markdown")
		gc.Add("robpike/ivy")
		gc.Add("rsc/top")
		gc.Add("rsc/omap")
		gc.Add("golang/go")
	*/
	dc := docs.New(db)
	ai, err := gemini.NewClient(lg, sdb, http.DefaultClient)
	if err != nil {
		log.Fatal(err)
	}

	if *searchMode {
		// Search loop.
		s := bufio.NewScanner(os.Stdin)
		for {
			fmt.Fprintf(os.Stderr, "> ")
			if !s.Scan() {
				break
			}
			vecs, err := ai.EmbedDocs([]llm.EmbedDoc{{Title: "", Text: s.Text()}})
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			vec := vecs[0]
			for _, r := range vdb.Search(vec, 20) {
				title := "?"
				if d, ok := dc.Get(r.ID); ok {
					title = d.Title
				}
				fmt.Printf(" %.5f %s # %s\n", r.Score, r.ID, title)
			}
		}
	}

	gh.Sync()
	githubdocs.Sync(lg, dc, gh)
	embeddocs.Sync(lg, vdb, ai, dc)

	cf := commentfix.New(lg, gh, "gerritlinks")
	cf.EnableProject("golang/go")
	cf.EnableEdits()
	cf.AutoLink(`\bCL ([0-9]+)\b`, "https://go.dev/cl/$1")
	cf.ReplaceURL(`\Qhttps://go-review.git.corp.google.com/\E`, "https://go-review.googlesource.com/")

	rp := related.New(lg, db, vdb, gh, dc, "related")
	rp.EnableProject("golang/go")
	rp.EnablePosts()
	rp.SkipBodyContains("— [watchflakes](https://go.dev/wiki/Watchflakes)")
	rp.SkipTitlePrefix("x/tools/gopls: release version v")
	rp.SkipTitleSuffix(" backport]")
	for {
		gh.Sync()
		githubdocs.Sync(lg, dc, gh)
		embeddocs.Sync(lg, vdb, ai, dc)
		cf.Run()
		rp.Run()
		time.Sleep(2 * time.Minute)
	}
	return
}
