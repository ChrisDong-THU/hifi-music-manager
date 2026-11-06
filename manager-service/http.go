package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func tagNumber(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(value, "/", 2)[0]))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

func albumYear(value string) int {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return 0
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year < 1 {
		return 0
	}
	return year
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	respond(w, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		problem(w, 400, errors.New("请求内容无效"))
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		problem(w, 400, errors.New("请求包含多余数据"))
		return false
	}
	return true
}
func (a *app) protect(next http.Handler) http.Handler {
	return a.protectManager(next, nil, nil)
}

func (a *app) protectManager(next http.Handler, auth *managerAuth, health func() managerHealth) http.Handler {
	return a.protectManagerHost(next, auth, health, "")
}

func (a *app) protectManagerHost(next http.Handler, auth *managerAuth, health func() managerHealth, publicDomain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		public := publicDomain != ""
		if public && !strings.EqualFold(strings.TrimSuffix(r.Host, ":443"), publicDomain) ||
			!public && host != "localhost" && !a.allowedAddress(host) {
			problem(w, 403, errors.New("不允许此访问主机"))
			return
		}
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || public && !loopbackAddress(remote) || !public && !a.allowedAddress(remote) {
			problem(w, 403, errors.New("不允许此网络访问"))
			return
		}
		if auth != nil && r.URL.Path == "/healthz" {
			if public {
				http.NotFound(w, r)
				return
			}
			serveManagerHealth(w, r, health)
			return
		}
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		} else if public || auth != nil && (!loopbackAddress(remote) || (host != "localhost" && !loopbackAddress(host))) {
			problem(w, http.StatusForbidden, errors.New("局域网管理需要使用 HTTPS"))
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, e := url.Parse(origin)
				if e != nil || u.Scheme != scheme ||
					public && !strings.EqualFold(strings.TrimSuffix(u.Host, ":443"), publicDomain) || !public && u.Host != r.Host {
					problem(w, 403, errors.New("请求来源不匹配"))
					return
				}
			}
			if site := r.Header.Get("Sec-Fetch-Site"); auth != nil && (site == "cross-site" || site == "same-site") {
				problem(w, 403, errors.New("请求来源不匹配"))
				return
			}
		}
		if auth != nil && !auth.allow(w, r, "hifi_auth_"+strings.TrimPrefix(a.cookieName, "hifi_session_")) {
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			cookie, e := r.Cookie(a.cookieName)
			if e != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(a.csrf)) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(a.csrf)) != 1 {
				problem(w, 403, errors.New("会话已过期，请刷新页面"))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if err := a.checkStorage(); err != nil {
				problem(w, 503, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *app) routes(site http.Handler) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/state", a.state)
	m.HandleFunc("GET /api/system/disks", func(w http.ResponseWriter, r *http.Request) {
		respond(w, map[string]any{"enabled": false, "disks": []any{}})
	})
	a.libraryRoutes(m)
	a.draftReadRoutes(m)
	a.qualityRoutes(m)
	m.HandleFunc("POST /api/scan", func(w http.ResponseWriter, r *http.Request) {
		id, err := a.submit("scan", a.scan)
		jobResponse(w, id, err)
	})
	m.HandleFunc("GET /api/scan", a.scanState)
	m.HandleFunc("POST /api/drafts", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len(in.Name) > 300 {
			problem(w, 400, errors.New("请输入批次名称"))
			return
		}
		b := Batch{ID: uid(), Name: in.Name, Created: now(), State: "uploading", ReadState: "queued", QualityState: "queued", Files: []Track{}}
		b.UploadOwner = b.ID
		if err := a.put("batches", b.ID, b); err != nil {
			problem(w, 500, err)
			return
		}
		respond(w, map[string]string{"id": b.ID})
	})
	m.HandleFunc("POST /api/drafts/{id}/files", a.upload)
	m.HandleFunc("POST /api/drafts/{id}/discard", func(w http.ResponseWriter, r *http.Request) {
		var options struct{}
		if !decode(w, r, &options) {
			return
		}
		batch := r.PathValue("id")
		id, err := a.submitForBatch("draft-delete", batch, func() error { return a.discardEmptyDraft(batch) })
		jobResponse(w, id, err)
	})
	m.HandleFunc("POST /api/drafts/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		var options struct{}
		if !decode(w, r, &options) {
			return
		}
		id, err := a.submitDraftTask(r.PathValue("id"), "preview")
		jobResponse(w, id, err)
	})
	m.HandleFunc("POST /api/drafts/{id}/tags", func(w http.ResponseWriter, r *http.Request) {
		if err := a.checkDraftIdle(r.PathValue("id")); err != nil {
			problem(w, 409, err)
			return
		}
		tags, cover, ok := decodeDraft(w, r)
		if !ok {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if err := a.checkDraftIdle(r.PathValue("id")); err != nil {
			problem(w, 409, err)
			return
		}
		b, err := get[Batch](a, "batches", r.PathValue("id"))
		if err != nil || b.State == "imported" || b.State == "grouped" || b.State == "deleted" || len(tags) != len(b.Files) || len(tags) == 0 {
			problem(w, 400, errors.New("预览数据与草稿不匹配"))
			return
		}
		if err := validateDraftSnapshot(b, r.Header.Get("If-Match")); err != nil {
			commandProblem(w, err)
			return
		}
		for i, t := range tags {
			if cover != nil && !b.Files[i].Editable {
				problem(w, 400, errors.New("该专辑含暂不支持写入封面的格式，目前支持 FLAC 和 MP3"))
				return
			}
			if tags[i], err = patchTags(t, tagValues(t)); err != nil {
				problem(w, 400, err)
				return
			}
			if !b.Files[i].Editable && t != b.Files[i].Tags {
				problem(w, 400, errors.New("此格式仅支持保留原始标签"))
				return
			}
		}
		c := Candidate{ID: "manual", Source: "manual", Tags: tags}
		b.Candidates = []Candidate{c}
		b.ChosenCandidate = "manual"
		b.Previewed = true
		b.State = "ready"
		b.Message = "标签已保存，等待导入"
		if err = a.putDraft(b, cover, false); err != nil {
			problem(w, 500, err)
			return
		}
		respond(w, map[string]any{"tags": c.Tags})
	})
	m.HandleFunc("POST /api/drafts/{id}/import", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			DuplicatePolicy string   `json:"duplicate_policy"`
			IDs             []string `json:"ids"`
		}
		if !decode(w, r, &in) || !validIDs(w, in.IDs) {
			return
		}
		batch := r.PathValue("id")
		id, err := a.submitDraftChange("import", batch, r.Header.Get("If-Match"), func() error {
			return a.importLocked(batch, in.DuplicatePolicy, in.IDs)
		})
		jobResponse(w, id, err)
	})
	m.HandleFunc("POST /api/drafts/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if !decode(w, r, &in) || !validIDs(w, in.IDs) {
			return
		}
		batch := r.PathValue("id")
		id, err := a.submitDraftChange("draft-delete", batch, r.Header.Get("If-Match"), func() error { return a.deleteDraftLocked(batch, in.IDs) })
		jobResponse(w, id, err)
	})
	m.HandleFunc("POST /api/tracks/edit", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs   []string          `json:"ids"`
			Patch map[string]string `json:"patch"`
		}
		if !decode(w, r, &in) || !validIDs(w, in.IDs) {
			return
		}
		id, err := a.submit("edit", func() error { return a.edit(in.IDs, in.Patch) })
		jobResponse(w, id, err)
	})
	m.HandleFunc("POST /api/tracks/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if !decode(w, r, &in) || !validIDs(w, in.IDs) {
			return
		}
		id, err := a.submit("delete", func() error { return a.deleteTracks(in.IDs) })
		jobResponse(w, id, err)
	})
	m.HandleFunc("GET /api/tracks/{id}/audio", func(w http.ResponseWriter, r *http.Request) {
		t, err := get[Track](a, "tracks", r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err = regular(a.music, t.Path); err != nil {
			problem(w, 404, err)
			return
		}
		f, err := a.music.Open(t.Path)
		if err != nil {
			problem(w, 404, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			problem(w, 500, err)
			return
		}
		http.ServeContent(w, r, filepath.Base(t.Path), st.ModTime(), f)
	})
	m.HandleFunc("GET /api/tracks/{id}", func(w http.ResponseWriter, r *http.Request) {
		t, err := get[Track](a, "tracks", r.PathValue("id"))
		if err != nil {
			problem(w, 404, errors.New("曲目不存在"))
			return
		}
		respond(w, t)
	})
	m.HandleFunc("GET /api/tracks/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		t, err := get[Track](a, "tracks", r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		serveFileCover(w, r, a.music, t.Path)
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, errors.New("接口不存在")) })
	m.Handle("/", site)
	a.workflowRoutes(m)
	a.albumEditRoutes(m)
	a.artistProfileRoutes(m)
	a.draftCoverRoutes(m)
	return a.scopeLibrary(m)
}
func jobResponse(w http.ResponseWriter, id string, err error) {
	if err != nil {
		commandProblem(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	respond(w, map[string]string{"job": id})
}

func commandProblem(w http.ResponseWriter, err error) {
	status := http.StatusConflict
	if errors.Is(err, errDraftSnapshotRequired) {
		status = http.StatusPreconditionRequired
	}
	if errors.Is(err, errDraftSnapshotStale) {
		status = http.StatusPreconditionFailed
	}
	problem(w, status, err)
}
func validIDs(w http.ResponseWriter, ids []string) bool {
	if len(ids) == 0 || len(ids) > 300 {
		problem(w, 400, errors.New("请选择 1 至 300 首曲目"))
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 4096 {
			problem(w, 400, errors.New("曲目标识无效"))
			return false
		}
		if seen[id] {
			problem(w, 400, errors.New("曲目重复"))
			return false
		}
		seen[id] = true
	}
	return true
}
func (a *app) upload(w http.ResponseWriter, r *http.Request) {
	if err := a.checkDraftIdle(r.PathValue("id")); err != nil {
		problem(w, 409, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDraftIdle(r.PathValue("id")); err != nil {
		problem(w, 409, err)
		return
	}
	b, err := get[Batch](a, "batches", r.PathValue("id"))
	if err != nil {
		problem(w, 404, err)
		return
	}
	if err := a.checkDraftWritable(b.ID); err != nil {
		problem(w, 409, err)
		return
	}
	if b.ParentID != "" || len(b.Children) > 0 {
		problem(w, 409, errors.New("分组后的草稿不能追加文件，请新建上传"))
		return
	}
	p, err := cleanRelative(r.URL.Query().Get("path"))
	if err != nil {
		problem(w, 400, err)
		return
	}
	if !audioExtensions[strings.ToLower(filepath.Ext(p))] {
		problem(w, 400, errors.New("请选择 FLAC、MP3、M4A、OGG、OPUS、WAV 或 AIFF 文件"))
		return
	}
	dst := uploadsPrefix + b.ID + "/" + p
	// A completed upload may lose its response. An identical retry is safe;
	// different content must never overwrite a previously uploaded file.
	for _, existing := range b.Files {
		if existing.Path != dst {
			continue
		}
		hash := sha256.New()
		n, err := io.Copy(hash, http.MaxBytesReader(w, r.Body, 2<<30))
		if err != nil {
			problem(w, 400, errors.New("上传未完成或超过单文件 2 GiB 限制"))
			return
		}
		if n == existing.Size && fmt.Sprintf("%x", hash.Sum(nil)) == existing.Hash {
			if current, err := fileHash(a.data, dst); err == nil && current == existing.Hash {
				respond(w, existing)
				return
			}
		}
		problem(w, 409, errors.New("该批次已存在不同内容的同名文件"))
		return
	}
	if len(b.Files) >= 300 {
		problem(w, 400, errors.New("每个批次最多 300 首"))
		return
	}
	if err = ensureDir(a.data, filepath.Dir(dst)); err != nil {
		problem(w, 400, err)
		return
	}
	if _, e := a.data.Lstat(dst); !errors.Is(e, os.ErrNotExist) {
		problem(w, 409, errors.New("该批次已存在同名文件"))
		return
	}
	tmp := dst + ".upload-" + uid()
	f, err := a.data.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		problem(w, 500, err)
		return
	}
	defer a.data.Remove(tmp)
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	_, err = io.Copy(f, r.Body)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		problem(w, 400, errors.New("上传未完成或超过单文件 2 GiB 限制"))
		return
	}
	if err = a.data.Rename(tmp, dst); err != nil {
		problem(w, 500, err)
		return
	}
	t, err := inspectContext(r.Context(), a.data, dst)
	if err != nil {
		_ = a.data.Remove(dst)
		problem(w, 400, fmt.Errorf("文件校验失败：%w", err))
		return
	}
	b.Files = append(b.Files, t)
	b.ReadState, b.ReadError, b.QualityState, b.QualityError = "queued", "", "queued", ""
	b.Candidates = nil
	b.ChosenCandidate = "original"
	b.State = "ready"
	b.Message = "上传完成，等待检查预览"
	if err = a.put("batches", b.ID, b); err != nil {
		_ = a.data.Remove(dst)
		problem(w, 500, err)
		return
	}
	respond(w, t)
}
