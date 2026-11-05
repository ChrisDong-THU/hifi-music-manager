package main

import (
	"errors"
	"net/http"
	"strings"
)

const librarySessionHeader = "X-Library-Session"

// CSRF identifies the browser's service session. This separate precondition
// binds resource operations to one opening of one library, including remounts
// and service restarts. It cannot be inferred from a track ID or database path.
func (a *app) scopeLibrary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/state" && r.URL.Path != "/api/system/disks" {
			session := r.Header.Get(librarySessionHeader)
			if session == "" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				// Native audio/image requests cannot set application headers.
				session = r.URL.Query().Get("library_session")
			}
			mutation := r.Method != http.MethodGet && r.Method != http.MethodHead
			if (mutation || session != "") && session != a.librarySession {
				problem(w, http.StatusConflict, errors.New("曲库已切换或重新打开，请刷新后重试"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
