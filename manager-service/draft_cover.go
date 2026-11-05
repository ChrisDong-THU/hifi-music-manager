package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/dhowden/tag"
)

func draftArtworkDirectory(b Batch) (string, error) {
	root, err := draftFileRoot(b)
	if err != nil || !hexID(b.ID, 32) || !hexID(path.Base(root), 32) {
		return "", errors.New("草稿封面目录无效")
	}
	return path.Join(root, ".artwork", b.ID), nil
}

func draftArtworkPath(b Batch) (string, error) {
	dir, err := draftArtworkDirectory(b)
	if err != nil || !hexID(b.CoverHash, 64) {
		return "", errors.New("草稿封面标识无效")
	}
	return path.Join(dir, b.CoverHash), nil
}

// Publish the complete immutable file first, then commit its database reference.
// A crash can only leave an unreferenced file, which cleanup later reclaims.
func (a *app) putDraft(b Batch, cover *albumCover, resetCover bool) error {
	if err := a.checkDraftWritable(b.ID); err != nil {
		return err
	}
	previous, err := get[Batch](a, "batches", b.ID)
	if err != nil {
		return err
	}
	if cover != nil {
		hash := sha256.Sum256(cover.Data)
		b.CoverHash = hex.EncodeToString(hash[:])
		p, err := draftArtworkPath(b)
		if err != nil {
			return err
		}
		if err := a.writeDraftArtwork(p, cover.Data); err != nil {
			return err
		}
	} else if resetCover {
		b.CoverHash = ""
	}
	if previous.CoverHash != "" && previous.CoverHash != b.CoverHash {
		p, err := draftArtworkPath(previous)
		if err != nil {
			return err
		}
		b.Cleanup = append(b.Cleanup, p)
	}
	if err := a.put("batches", b.ID, b); err != nil {
		return err
	}
	if err := a.cleanupBatchLocked(&b); err != nil {
		a.wakeMaintenance()
		return err
	}
	return nil
}

func (a *app) writeDraftArtwork(p string, data []byte) error {
	if len(data) > maxAlbumCover {
		return errors.New("草稿封面过大")
	}
	if err := ensureDir(a.data, path.Dir(p)); err != nil {
		return err
	}
	if info, err := a.data.Lstat(p); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("封面位置已被占用")
		}
		hash, err := fileHash(a.data, p)
		if err != nil || hash != path.Base(p) {
			return errors.New("已有封面文件校验失败")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := p + ".pending-" + uid()
	f, err := a.data.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer a.data.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return a.data.Rename(tmp, p)
}

func (a *app) draftCover(b Batch) (*albumCover, error) {
	if b.CoverHash == "" {
		return nil, nil
	}
	p, err := draftArtworkPath(b)
	if err != nil {
		return nil, err
	}
	if err := regular(a.data, p); err != nil {
		return nil, err
	}
	f, err := a.data.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAlbumCover+1))
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != b.CoverHash {
		return nil, errors.New("草稿封面读取失败，请重新上传图片")
	}
	return parseAlbumCover(data)
}

func decodeDraft(w http.ResponseWriter, r *http.Request) ([]Tags, *albumCover, bool) {
	var in struct {
		Tags []Tags `json:"tags"`
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		ok := decode(w, r, &in)
		return in.Tags, nil, ok
	}
	form, cover, err := decodeImageForm(w, r, "cover", 2<<20)
	if err != nil {
		problem(w, 400, err)
		return nil, nil, false
	}
	if cover == nil || json.Unmarshal([]byte(form.Get("tags")), &in.Tags) != nil {
		problem(w, 400, errors.New("草稿标签或封面无效"))
		return nil, nil, false
	}
	return in.Tags, cover, true
}

func serveFileCover(w http.ResponseWriter, r *http.Request, root *os.Root, path string) {
	if regular(root, path) != nil {
		http.NotFound(w, r)
		return
	}
	f, err := root.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	metadata, err := tag.ReadFrom(&boundedMetadataReader{ReadSeeker: f, remaining: 64 << 20})
	if err != nil || metadata.Picture() == nil {
		http.NotFound(w, r)
		return
	}
	p := metadata.Picture()
	mime := http.DetectContentType(p.Data)
	if len(p.Data) > maxAlbumCover || !(mime == "image/jpeg" || mime == "image/png" || mime == "image/gif" || mime == "image/webp") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(p.Data)
}

func (a *app) draftCoverRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/drafts/{id}/cover", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		b, err := get[Batch](a, "batches", r.PathValue("id"))
		if err != nil {
			a.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		if b.CoverHash != "" && b.ChosenCandidate == "manual" {
			cover, err := a.draftCover(b)
			a.mu.Unlock()
			if err != nil {
				problem(w, 500, err)
				return
			}
			w.Header().Set("Content-Type", cover.MIME)
			_, _ = w.Write(cover.Data)
			return
		}
		a.mu.Unlock()
		if len(b.Files) == 0 {
			http.NotFound(w, r)
			return
		}
		serveFileCover(w, r, a.data, b.Files[0].Path)
	})
}
