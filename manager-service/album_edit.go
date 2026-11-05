package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
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

var albumFields = []string{"album", "album_artist", "genre", "year"}

func albumTagValues(tags Tags) map[string]string {
	return map[string]string{"album": tags.Album, "album_artist": tags.AlbumArtist, "genre": tags.Genre, "year": tags.Year}
}

func (a *app) editAlbum(key string, patch map[string]string, cover *albumCover) error {
	a.mu.Lock()
	defer a.mu.Unlock()
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
	tracks, err := a.readAlbumTracks(key)
	if err != nil {
		return err
	}
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

func (a *app) albumEditRoutes(m *http.ServeMux) {
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
		id, err := a.submit("delete", func() error { return a.deleteAlbum(in.Key) })
		jobResponse(w, id, err)
	})
	m.HandleFunc("GET /api/albums/detail", func(w http.ResponseWriter, r *http.Request) {
		tracks, err := a.readAlbumTracks(r.URL.Query().Get("key"))
		if err != nil {
			problem(w, 500, err)
			return
		}
		if len(tracks) == 0 {
			problem(w, 404, errors.New("专辑不存在"))
			return
		}
		tags := map[string]string{}
		mixed := []string{}
		first := albumTagValues(tracks[0].Tags)
		for _, field := range albumFields {
			tags[field] = first[field]
			for _, track := range tracks[1:] {
				values := albumTagValues(track.Tags)
				if values[field] != tags[field] {
					tags[field] = ""
					mixed = append(mixed, field)
					break
				}
			}
		}
		editable := true
		for _, track := range tracks {
			editable = editable && track.Editable
		}
		respond(w, map[string]any{"tags": tags, "mixed": mixed, "count": len(tracks), "editable": editable, "cover_id": tracks[0].ID, "cover_version": tracks[0].Hash})
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
		id, err := a.submit("edit", func() error { return a.editAlbum(key, patch, cover) })
		jobResponse(w, id, err)
	})
}

func (a *app) deleteAlbum(key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := []string{}
	tracks, err := a.readAlbumTracks(key)
	if err != nil {
		return err
	}
	for _, track := range tracks {
		ids = append(ids, track.ID)
	}
	if len(ids) == 0 {
		return errors.New("专辑不存在或已变更，请刷新后重试")
	}
	return a.deleteTracksLocked(ids)
}
