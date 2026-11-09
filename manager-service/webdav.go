package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

const (
	davPath     = "/webdav"
	davSettings = "/api/system/webdav"
	davUser     = "hifi"
	davMethods  = "OPTIONS, PROPFIND, GET, HEAD"
)

type musicDAV struct {
	password string
	digest   [sha256.Size]byte
}

func loadMusicDAV(home string) (*musicDAV, error) {
	// The caller holds the manager auth DB lock while provisioning this key.
	key, err := loadServiceKey(filepath.Join(home, "config"), "webdav.key")
	if err != nil {
		return nil, err
	}
	password := base64.RawURLEncoding.EncodeToString(key)
	return &musicDAV{password: password, digest: sha256.Sum256([]byte(password))}, nil
}

func isDAVPath(p string) bool { return p == davPath || strings.HasPrefix(p, davPath+"/") }

func (d *musicDAV) serve(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if r.Method == http.MethodOptions {
		w.Header().Set("DAV", "1")
		w.Header().Set("Allow", davMethods)
		w.WriteHeader(http.StatusOK)
		return
	}
	user, password, ok := r.BasicAuth()
	digest := sha256.Sum256([]byte(password))
	if !ok || user != davUser || subtle.ConstantTimeCompare(digest[:], d.digest[:]) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="HiFi WebDAV"`)
		http.Error(w, "WebDAV credentials required", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case "PROPFIND":
		switch r.Header.Get("Depth") {
		case "", "infinity":
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `<D:error xmlns:D="DAV:"><D:propfind-finite-depth/></D:error>`)
			return
		case "0", "1":
		default:
			http.Error(w, "Invalid Depth", http.StatusBadRequest)
			return
		}
		if r.ContentLength > 64<<10 {
			http.Error(w, "PROPFIND body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	case http.MethodGet, http.MethodHead:
	default:
		w.Header().Set("Allow", davMethods)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// Validate the decoded path before the DAV handler normalizes it.
	if _, err := davLocalPath(strings.TrimPrefix(r.URL.Path, davPath)); err != nil {
		http.NotFound(w, r)
		return
	}
	next.ServeHTTP(w, r)
}

func (a *app) serveDAV(w http.ResponseWriter, r *http.Request) {
	handler := webdav.Handler{Prefix: davPath, FileSystem: davMusicFS{a.music}, LockSystem: webdav.NewMemLS()}
	handler.ServeHTTP(w, r)
}

func davLocalPath(name string) (string, error) {
	name = strings.TrimSuffix(strings.TrimPrefix(name, "/"), "/")
	if name == "" {
		return ".", nil
	}
	if !fs.ValidPath(name) || strings.Contains(name, `\`) {
		return "", fs.ErrNotExist
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fs.ErrNotExist
		}
	}
	return filepath.Localize(name)
}

type davMusicFS struct{ root *os.Root }

func davVisible(info os.FileInfo) bool {
	return !strings.HasPrefix(info.Name(), ".") && (info.IsDir() ||
		info.Mode().IsRegular() && audioExtensions[strings.ToLower(filepath.Ext(info.Name()))])
}

func (f davMusicFS) stat(ctx context.Context, name string) (string, os.FileInfo, error) {
	local, err := davLocalPath(name)
	if err != nil {
		return "", nil, fs.ErrNotExist
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	// Reject symlinks in every component, including links within the music root.
	// os.Root also prevents escapes if a directory changes between lookups.
	for parent := filepath.Dir(local); parent != "."; parent = filepath.Dir(parent) {
		info, err := f.root.Lstat(parent)
		if err != nil || !info.IsDir() {
			return "", nil, fs.ErrNotExist
		}
	}
	info, err := f.root.Lstat(local)
	if err != nil {
		return "", nil, err
	}
	if local != "." && !davVisible(info) {
		return "", nil, fs.ErrNotExist
	}
	return local, info, nil
}

func (f davMusicFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	_, info, err := f.stat(ctx, name)
	return info, err
}

func (f davMusicFS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	if flag != os.O_RDONLY {
		return nil, fs.ErrPermission
	}
	local, expected, err := f.stat(ctx, name)
	if err != nil {
		return nil, err
	}
	file, err := f.root.OpenFile(local, davReadFlags, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, expected) {
		_ = file.Close()
		return nil, fs.ErrNotExist
	}
	return &davReadFile{File: file, ctx: ctx}, nil
}

func (davMusicFS) Mkdir(context.Context, string, os.FileMode) error { return fs.ErrPermission }
func (davMusicFS) RemoveAll(context.Context, string) error          { return fs.ErrPermission }
func (davMusicFS) Rename(context.Context, string, string) error     { return fs.ErrPermission }

type davReadFile struct {
	*os.File
	ctx context.Context
}

func (*davReadFile) Write([]byte) (int, error) { return 0, fs.ErrPermission }

func (f *davReadFile) Read(buf []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.File.Read(buf)
}

func (f *davReadFile) Readdir(n int) ([]os.FileInfo, error) {
	var result []os.FileInfo
	for n <= 0 || len(result) < n {
		if err := f.ctx.Err(); err != nil {
			return nil, err
		}
		count := 128
		if n > 0 {
			count = min(count, n-len(result))
		}
		entries, err := f.File.Readdir(count)
		for _, entry := range entries {
			if davVisible(entry) {
				result = append(result, entry)
			}
		}
		if errors.Is(err, io.EOF) && (n <= 0 || len(result) > 0) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
