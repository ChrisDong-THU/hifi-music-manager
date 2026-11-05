package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneBinaryIncludesUIAndDependencyNotices(t *testing.T) {
	for _, path := range []string{"/", "/brand.svg", "/THIRD_PARTY_NOTICES.txt", "/licenses/beets-flask-MIT.txt", "/licenses/frontend.txt", "/licenses/flaccompagnon-MPL-2.0.txt", "/licenses/flaccompagnon-source.txt"} {
		w := httptest.NewRecorder()
		staticSite().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal(path, w.Code)
		}
		if strings.HasSuffix(path, ".txt") && strings.Contains(w.Body.String(), "<html") {
			t.Fatal("license route returned the app shell", path)
		}
	}
}

func TestAdaptedAnalysisSourceNoticeMatchesShippedCode(t *testing.T) {
	notice, err := assets.ReadFile("licenses/flaccompagnon-source.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"bitdepth.go", "spectrum.go", "analysis.go"} {
		source, err := os.ReadFile(filepath.Join("internal", "audioquality", file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(notice), strings.TrimSpace(string(source))) {
			t.Fatalf("source notice is stale for %s; regenerate with npm run build", file)
		}
	}
}
