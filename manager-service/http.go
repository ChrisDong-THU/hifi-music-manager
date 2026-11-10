package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
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
func (a *app) protectManager(next http.Handler, auth *managerAuth, health func() managerHealth, publicDomain, lanHostname string) http.Handler {
	return a.protectManagerDAV(next, auth, health, publicDomain, lanHostname, nil)
}

func (a *app) protectManagerDAV(next http.Handler, auth *managerAuth, health func() managerHealth, publicDomain, lanHostname string, dav *musicDAV) http.Handler {
	public := publicDomain != ""
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
		lanName := lanHostname != "" && strings.EqualFold(host, lanHostname)
		if public && !strings.EqualFold(strings.TrimSuffix(r.Host, ":443"), publicDomain) ||
			!public && host != "localhost" && !lanName && !a.allowedAddress(host) {
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
		davRequest := isDAVPath(r.URL.Path)
		if davRequest || r.Method != "GET" && r.Method != "HEAD" {
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
		if davRequest {
			if dav == nil {
				http.NotFound(w, r)
			} else {
				dav.serve(w, r, next)
			}
			return
		}
		if auth != nil && !auth.allow(w, r, "hifi_auth_"+strings.TrimPrefix(a.cookieName, "hifi_session_")) {
			return
		}
		if auth != nil && r.URL.Path == davSettings && r.Method == http.MethodGet {
			if dav == nil {
				respond(w, map[string]bool{"enabled": false})
			} else {
				respond(w, map[string]any{"enabled": true, "url": scheme + "://" + r.Host + davPath + "/",
					"username": davUser, "password": dav.password})
			}
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
		tags, albumMatch, cover, ok := decodeDraft(w, r)
		if !ok {
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		b, err := get[Batch](a, "batches", r.PathValue("id"))
		if err != nil {
			problem(w, 409, err)
			return
		}
		if a.draftTaskActive(b) {
			problem(w, 409, errDraftBusy)
			return
		}
		if !visibleDraft(b) || len(tags) != len(b.Files) || len(tags) == 0 {
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
			if !b.Files[i].Editable {
				if t != b.Files[i].Tags {
					problem(w, 400, errors.New("此格式仅支持保留原始标签"))
					return
				}
				continue
			}
			if tags[i], err = patchTags(t, tagValues(t)); err != nil {
				problem(w, 400, err)
				return
			}
		}
		if albumMatch != "" {
			if albumMatch != "new" && !hexID(albumMatch, 64) {
				problem(w, 400, errors.New("专辑匹配标识无效"))
				return
			}
			index, err := a.readLibraryIndex()
			if err != nil {
				problem(w, 500, err)
				return
			}
			identity := albumIdentity(tags[0].Album, albumCredit(tags[0]))
			for _, t := range tags {
				if strings.TrimSpace(t.Album) == "" || strings.TrimSpace(albumCredit(t)) == "" ||
					albumIdentity(t.Album, albumCredit(t)) != identity || albumMatch == "new" && editionYear(t.Year) != editionYear(tags[0].Year) {
					problem(w, 400, errors.New("请选择同一专辑的曲目，独立版本须使用同一发行年份"))
					return
				}
			}
			if _, err := chooseAlbum(index.byIdentity[identity], albumMatch, tags[0].Year); err != nil {
				problem(w, 409, err)
				return
			}
		}
		if cover != nil && !oneDraftArtworkRelease(tags, albumMatch) {
			problem(w, 400, errors.New("该草稿包含多张专辑，请按专辑整理后再上传封面"))
			return
		}
		c := Candidate{ID: "manual", Source: "manual", Tags: tags}
		b.Candidates = []Candidate{c}
		b.ChosenCandidate = "manual"
		b.AlbumMatch = albumMatch
		b.Previewed = true
		b.State = "ready"
		b.Message = "标签已保存，等待导入"
		if err = a.putDraft(b, cover, false); err != nil {
			problem(w, 500, err)
			return
		}
		respond(w, map[string]any{"tags": c.Tags, "album_match": b.AlbumMatch})
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
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		position, ok := index.byTrackID[r.PathValue("id")]
		if !ok {
			problem(w, 404, errors.New("曲目不存在"))
			return
		}
		respond(w, index.projectTrack(position))
	})
	m.HandleFunc("GET /api/tracks/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		position, ok := index.byTrackID[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		track := index.tracks[position]
		cover := (artworkSource{path: track.Path}).read(r.Context(), a.music)
		if cover == nil {
			cover = a.releaseArtwork(r.Context(), index, index.byAlbum[index.metadata[position].album])
		}
		serveArtwork(w, r, cover)
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
