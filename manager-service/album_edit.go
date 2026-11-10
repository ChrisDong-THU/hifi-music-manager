package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"sort"
	"strings"
)

const maxAlbumCover = 10 << 20

type albumCover struct {
	Data          []byte
	MIME          string
	Width, Height int
}

func parseAlbumCover(data []byte) (*albumCover, error) {
	if len(data) == 0 || len(data) > maxAlbumCover {
		return nil, errors.New("封面图片最大为 10 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errors.New("请选择有效的 JPG 或 PNG 图片")
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 24_000_000 {
		return nil, errors.New("图片尺寸过大，最长边不超过 8192 像素，且不超过 2400 万像素")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return nil, errors.New("封面图片损坏，无法读取")
	}
	return &albumCover{Data: data, MIME: "image/" + format, Width: cfg.Width, Height: cfg.Height}, nil
}

func (c *albumCover) flacPicture() []byte {
	var b bytes.Buffer
	write := func(n int) { _ = binary.Write(&b, binary.BigEndian, uint32(n)) }
	write(3)
	write(len(c.MIME))
	b.WriteString(c.MIME)
	write(0)
	write(c.Width)
	write(c.Height)
	write(24)
	write(0)
	write(len(c.Data))
	b.Write(c.Data)
	return b.Bytes()
}

var albumFields = [...]string{"album", "album_artist", "genre", "year"}

func albumTagValues(tags Tags) [len(albumFields)]string {
	return [...]string{tags.Album, tags.AlbumArtist, tags.Genre, tags.Year}
}

func (a *app) editAlbumLocked(tracks []Track, patch map[string]string, cover *albumCover) error {
	for field := range patch {
		if field != "album" && field != "album_artist" && field != "genre" && field != "year" {
			return errors.New("专辑编辑包含无效字段")
		}
	}
	if len(patch) == 0 && cover == nil {
		return errors.New("没有需要保存的修改")
	}
	ids := []string{}
	name := "未分类"
	for _, track := range tracks {
		ids = append(ids, track.ID)
		if track.Tags.Album != "" {
			name = track.Tags.Album
		}
	}
	if len(ids) == 0 {
		return errors.New("专辑不存在或已变更，请刷新后重试")
	}
	return a.editLocked(ids, patch, cover, "修改专辑 · "+name)
}

var (
	errAlbumSnapshotRequired = errors.New("请先读取专辑再提交操作")
	errAlbumSnapshotStale    = errors.New("专辑内容已变化，请重新打开后提交")
)

func albumEditToken(key string, tracks []Track) string {
	type member struct{ ID, Path, Hash string }
	members := make([]member, len(tracks))
	for i, track := range tracks {
		members[i] = member{track.ID, track.Path, track.Hash}
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	raw, _ := json.Marshal(struct {
		Key     string
		Members []member
	}{key, members})
	return fmt.Sprintf(`"album-%x"`, sha256.Sum256(raw))
}

func (a *app) albumSnapshot(key, token string) ([]Track, error) {
	if token == "" {
		return nil, errAlbumSnapshotRequired
	}
	tracks, err := a.readAlbumTracks(key)
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 || albumEditToken(key, tracks) != token {
		return nil, errAlbumSnapshotStale
	}
	return tracks, nil
}

func (a *app) submitAlbumChange(w http.ResponseWriter, r *http.Request, kind, key string, runLocked func([]Track) error) {
	token := r.Header.Get("If-Match")
	if _, err := a.albumSnapshot(key, token); err != nil {
		switch {
		case errors.Is(err, errAlbumSnapshotRequired):
			problem(w, http.StatusPreconditionRequired, err)
		case errors.Is(err, errAlbumSnapshotStale):
			problem(w, http.StatusPreconditionFailed, err)
		default:
			problem(w, http.StatusInternalServerError, err)
		}
		return
	}
	id, err := a.submit(kind, func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		tracks, err := a.albumSnapshot(key, token)
		if err != nil {
			return err
		}
		return runLocked(tracks)
	})
	jobResponse(w, id, err)
}

func (a *app) albumEditRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/albums/cover", func(w http.ResponseWriter, r *http.Request) {
		index, err := a.readLibraryIndex()
		if err != nil {
			problem(w, 500, err)
			return
		}
		release := index.byAlbum[r.URL.Query().Get("key")]
		serveArtwork(w, r, a.releaseArtwork(r.Context(), index, release))
	})
	m.HandleFunc("POST /api/albums/delete", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key string `json:"key"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Key == "" || len(in.Key) > 4096 {
			problem(w, 400, errors.New("专辑标识无效"))
			return
		}
		a.submitAlbumChange(w, r, "delete", in.Key, func(tracks []Track) error {
			ids := make([]string, 0, len(tracks))
			for _, track := range tracks {
				ids = append(ids, track.ID)
			}
			return a.deleteTracksLocked(ids)
		})
	})
	m.HandleFunc("GET /api/albums/detail", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		tracks, err := a.readAlbumTracks(key)
		if err != nil {
			problem(w, 500, err)
			return
		}
		if len(tracks) == 0 {
			problem(w, 404, errors.New("专辑不存在"))
			return
		}
		token := albumEditToken(key, tracks)
		w.Header().Set("ETag", token)
		if r.Header.Get("If-None-Match") == token {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		tags := map[string]string{}
		mixed := []string{}
		first := albumTagValues(tracks[0].Tags)
		var differs [len(albumFields)]bool
		editable := tracks[0].Editable
		for _, track := range tracks[1:] {
			editable = editable && track.Editable
			for i, value := range albumTagValues(track.Tags) {
				differs[i] = differs[i] || value != first[i]
			}
		}
		for i, field := range albumFields {
			if differs[i] {
				mixed = append(mixed, field)
				first[i] = ""
			}
			tags[field] = first[i]
		}
		respond(w, map[string]any{"tags": tags, "mixed": mixed, "count": len(tracks), "editable": editable, "edit_token": token})
	})
	m.HandleFunc("POST /api/albums/edit", func(w http.ResponseWriter, r *http.Request) {
		form, cover, err := decodeImageForm(w, r, "cover", 1<<20)
		if err != nil {
			problem(w, 400, err)
			return
		}
		key := form.Get("key")
		if key == "" || len(key) > 4096 {
			problem(w, 400, errors.New("专辑标识无效"))
			return
		}
		patch := map[string]string{}
		if err := json.Unmarshal([]byte(form.Get("patch")), &patch); err != nil {
			problem(w, 400, errors.New("专辑字段无效"))
			return
		}
		for _, field := range albumFields {
			if value, ok := patch[field]; ok {
				patch[field] = strings.TrimSpace(value)
			}
		}
		a.submitAlbumChange(w, r, "edit", key, func(tracks []Track) error {
			return a.editAlbumLocked(tracks, patch, cover)
		})
	})
}
