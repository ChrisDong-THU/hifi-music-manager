package main

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	library "hifi.local/library"
)

type ArtistProfile library.Artist

func (p ArtistProfile) imageURL() string {
	if p.ImageHash == "" {
		return ""
	}
	return "/api/artists/image?name=" + url.QueryEscape(p.Name) + "&v=" + p.ImageHash
}

func (a *app) readArtistProfile(name string) (ArtistProfile, error) {
	shared, err := library.LoadArtist(a.artists, name)
	profile := ArtistProfile(shared)
	if errors.Is(err, os.ErrNotExist) {
		return ArtistProfile{Name: name}, nil
	}
	return profile, err
}

func (a *app) hasArtist(name string) (bool, error) {
	index, err := a.readLibraryIndex()
	if err != nil {
		return false, err
	}
	return len(index.byAlbumArtist[name]) > 0, nil
}

func (a *app) saveArtistProfile(name, biography string, picture *albumCover) (ArtistProfile, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	exists, err := a.hasArtist(name)
	if err != nil {
		return ArtistProfile{}, err
	}
	if name == "" || len(name) > 4096 || strings.ContainsRune(name, 0) || !exists {
		return ArtistProfile{}, errors.New("歌手不存在，请刷新后重试")
	}
	if len(biography) > 16000 || strings.ContainsRune(biography, 0) {
		return ArtistProfile{}, errors.New("简介过长或包含无效字符")
	}
	profile, err := a.readArtistProfile(name)
	if err != nil {
		return ArtistProfile{}, err
	}
	profile.Biography = strings.TrimSpace(biography)
	var data []byte
	if picture != nil {
		data = picture.Data
		profile.ImageType = http.DetectContentType(data)
		profile.ImageHash = library.Hash(data)
	}
	if err := a.checkStorage(); err != nil {
		return ArtistProfile{}, err
	}
	op, err := a.begin("artist-edit", "修改歌手资料 · "+name)
	if err != nil {
		return ArtistProfile{}, err
	}
	shared, err := library.SaveArtist(a.artists, library.Artist(profile), data)
	if err != nil {
		return ArtistProfile{}, a.fail(&op, err)
	}
	op.Affected = 1
	if err := a.finish(&op); err != nil {
		return ArtistProfile{}, err
	}
	return ArtistProfile(shared), err
}

func respondArtistProfile(w http.ResponseWriter, profile ArtistProfile) {
	respond(w, map[string]string{"name": profile.Name, "biography": profile.Biography, "image_url": profile.imageURL()})
}

func (a *app) artistProfileRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/artists/detail", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		exists, err := a.hasArtist(name)
		if err != nil {
			problem(w, 500, err)
			return
		}
		if !exists {
			problem(w, 404, errors.New("歌手不存在"))
			return
		}
		profile, err := a.readArtistProfile(name)
		if err != nil {
			problem(w, 500, err)
			return
		}
		respondArtistProfile(w, profile)
	})
	m.HandleFunc("GET /api/artists/image", func(w http.ResponseWriter, r *http.Request) {
		profile, err := a.readArtistProfile(r.URL.Query().Get("name"))
		if err != nil {
			problem(w, 500, err)
			return
		}
		if hash := r.URL.Query().Get("v"); hash != "" {
			profile.ImageHash = hash
		}
		data, err := library.Picture(a.artists, library.Artist(profile))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		_, _ = w.Write(data)
	})
	m.HandleFunc("POST /api/artists/edit", func(w http.ResponseWriter, r *http.Request) {
		form, picture, err := decodeImageForm(w, r, "picture", 64<<10)
		if err != nil {
			problem(w, 400, err)
			return
		}
		profile, err := a.saveArtistProfile(form.Get("name"), form.Get("biography"), picture)
		if err != nil {
			problem(w, 400, err)
			return
		}
		respondArtistProfile(w, profile)
	})
}
