package main

import (
	"io/fs"
	"net/http"
)

// The deployed executable carries its complete UI and dependency notices.
// Both fixed-storage and disk-managed modes serve exactly the same assets.
func staticSite() http.Handler {
	web, _ := fs.Sub(assets, "web")
	licenses, _ := fs.Sub(assets, "licenses")
	m := http.NewServeMux()
	m.Handle("GET /licenses/", http.StripPrefix("/licenses/", http.FileServer(http.FS(licenses))))
	m.HandleFunc("GET /THIRD_PARTY_NOTICES.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, assets, "THIRD_PARTY_NOTICES.txt")
	})
	m.Handle("/", http.FileServer(http.FS(web)))
	return m
}
