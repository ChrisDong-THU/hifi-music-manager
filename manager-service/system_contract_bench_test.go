package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkControlStateWithDrafts(b *testing.B) {
	a, err := newApp(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer a.close()
	draft := Batch{ID: "draft", State: "ready"}
	for i := range 100 {
		draft.Files = append(draft.Files, Track{ID: fmt.Sprint(i), Tags: Tags{Title: fmt.Sprint(i), Lyrics: strings.Repeat("line ", 1600)}})
	}
	draft.Candidates = []Candidate{originalCandidate(draft), {ID: "manual", Source: "manual", Tags: originalCandidate(draft).Tags}}
	if err := a.put("batches", draft.ID, draft); err != nil {
		b.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	a.state(w, r)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		a.state(httptest.NewRecorder(), r)
	}
	b.ReportMetric(float64(w.Body.Len()), "response-B")
}
