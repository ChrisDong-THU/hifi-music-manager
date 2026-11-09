package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
	library "hifi.local/library"
)

type Tags struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"album_artist"`
	Genre       string `json:"genre"`
	Year        string `json:"year"`
	Track       string `json:"track"`
	Disc        string `json:"disc"`
	Lyrics      string `json:"lyrics"`
}

type Track struct {
	AudioInfo
	ID        string  `json:"id"`
	Path      string  `json:"path"`
	Tags      Tags    `json:"tags"`
	Format    string  `json:"format"`
	Size      int64   `json:"size"`
	Duration  float64 `json:"duration"`
	Hash      string  `json:"hash"`
	AudioHash string  `json:"audio_hash"`
	Editable  bool    `json:"editable"`
	Added     string  `json:"added"`
	Modified  int64   `json:"modified_ns,omitempty"`
}

type Album struct {
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Artist  string   `json:"artist"`
	Year    string   `json:"year"`
	Count   int      `json:"count"`
	CoverID string   `json:"cover_id"`
	Formats []string `json:"formats"`
	HiRes   bool     `json:"hi_res"`
	Added   string   `json:"added"`
}

func (t Track) hiResFLAC() bool {
	return t.Format == "FLAC" && t.BitDepth == 24 && t.SampleRate >= 44100 && t.SampleRate <= 192000
}

func albumKey(t Track) string {
	artist := t.Tags.AlbumArtist
	if artist == "" {
		artist = t.Tags.Artist
	}
	return artist + "\x00" + t.Tags.Album
}

type Candidate struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Tags   []Tags `json:"tags"`
}

type Batch struct {
	ReadState       string                   `json:"read_state,omitempty"`
	ReadError       string                   `json:"read_error,omitempty"`
	QualityError    string                   `json:"quality_error,omitempty"`
	QualityState    string                   `json:"quality_state,omitempty"`
	Quality         map[string]qualityResult `json:"quality,omitempty"`
	Cleanup         []string                 `json:"cleanup,omitempty"` // Forward-only deletion retries; no retained copies.
	UploadOwner     string                   `json:"upload_owner,omitempty"`
	CoverHash       string                   `json:"cover_hash,omitempty"`
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Created         string                   `json:"created"`
	State           string                   `json:"state"`
	Message         string                   `json:"message"`
	Files           []Track                  `json:"files"`
	Candidates      []Candidate              `json:"candidates"`
	Imported        []string                 `json:"imported"`
	ParentID        string                   `json:"parent_id,omitempty"`
	Children        []string                 `json:"children,omitempty"`
	ChosenCandidate string                   `json:"chosen_candidate,omitempty"`
	Previewed       bool                     `json:"previewed"`
	Duplicates      []string                 `json:"duplicates,omitempty"`
}

// Operation is a bounded audit summary, never a recovery plan.
type Operation struct {
	Sequence    uint64 `json:"sequence"`
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Created     string `json:"created"`
	CompletedAt string `json:"completed_at,omitempty"`
	State       string `json:"state"`
	Affected    int    `json:"affected"`
	Skipped     int    `json:"skipped,omitempty"`
	Error       string `json:"error,omitempty"`
}

type task struct {
	BatchID string `json:"batch_id,omitempty"`
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	State   string `json:"state"`
	Message string `json:"message"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
}
type work struct {
	id string
	fn func() error
}
type app struct {
	draftSubmitMu   sync.Mutex
	dataCaseFold    bool
	maintenanceWake chan struct{}
	ctx             context.Context
	requestMu       sync.Mutex
	requests        sync.WaitGroup
	mutations       atomic.Int32
	closing         bool
	indexMu         sync.Mutex
	index           *libraryIndex
	home            string
	db              *bolt.DB
	dbAnchor        *os.File
	music           *os.Root
	artists         *os.Root
	data            *os.Root
	mu              sync.Mutex // All file changes and index snapshots are serialized.
	jobMu           sync.Mutex
	jobs            map[string]*task
	scanning        *scanProgress // Protected by jobMu; never persisted as an index.
	queue           chan work
	wg              sync.WaitGroup
	csrf            string
	librarySession  string // Ephemeral open-library identity; never persisted.
	cookieName      string
	lan             netip.Prefix
	storageGuard    func() error
	storage         *storageInfo
	storageMountID  string
	externalTasks   func() []task
}

func uid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func now() string { return time.Now().Format(time.RFC3339) }
func newAppStorage(home, musicPath, dataPath string, guard func() error) (*app, error) {
	if guard != nil {
		if err := guard(); err != nil {
			return nil, err
		}
	}
	artistsPath := filepath.Join(filepath.Dir(musicPath), filepath.FromSlash(library.ArtistDirectory))
	for _, p := range []string{home, musicPath, dataPath, filepath.Join(dataPath, library.UploadsDirectory), artistsPath} {
		if guard == nil || (dataPath != home && p == home) {
			if err := os.MkdirAll(p, 0700); err != nil {
				return nil, err
			}
		}
		if st, err := os.Lstat(p); err != nil || st.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("数据目录不能是符号链接")
		}
	}
	dbPath := filepath.Join(home, library.DatabaseFile)
	var anchor *os.File
	if guard != nil && home == dataPath {
		var err error
		dbPath, anchor, err = anchoredDatabase(home)
		if err != nil {
			return nil, err
		}
		defer func() {
			if anchor != nil {
				anchor.Close()
			}
		}()
	}
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	music, err := os.OpenRoot(musicPath)
	if err != nil {
		db.Close()
		return nil, err
	}
	artists, err := os.OpenRoot(artistsPath)
	if err != nil {
		db.Close()
		music.Close()
		return nil, err
	}
	data, err := os.OpenRoot(dataPath)
	if err != nil {
		db.Close()
		music.Close()
		artists.Close()
		return nil, err
	}
	if guard != nil {
		if err := guard(); err != nil {
			db.Close()
			music.Close()
			data.Close()
			artists.Close()
			return nil, err
		}
	}
	a := &app{home: home, db: db, music: music, artists: artists, data: data, jobs: map[string]*task{}, queue: make(chan work, 16), csrf: uid(), librarySession: uid(), storageGuard: guard, maintenanceWake: make(chan struct{}, 1)}
	a.dbAnchor, anchor = anchor, nil
	key := sha256.Sum256([]byte(home))
	a.cookieName = "hifi_session_" + hex.EncodeToString(key[:6])
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{"tracks", "batches", "operations"} {
			if _, e := tx.CreateBucketIfNotExists([]byte(b)); e != nil {
				return e
			}
		}
		return trimOperationHistoryTx(tx)
	})
	if err != nil {
		a.close()
		return nil, err
	}
	a.dataCaseFold, err = detectCaseFold(a.data)
	if err != nil {
		a.close()
		return nil, err
	}
	if err := a.checkStorage(); err != nil {
		a.close()
		return nil, err
	}
	// Startup scanning rebuilds the index from published files. Interrupted writes
	// are reported honestly; there are no inverse operations or automatic rollback.
	ops, err := readAll[Operation](a, "operations")
	if err != nil {
		a.close()
		return nil, err
	}
	for _, op := range ops {
		if op.State == "working" {
			op.State, op.CompletedAt = "failed", now()
			op.Error = "服务中断；已完成的修改保留，请检查当前文件后继续"
			if err := a.saveOperation(&op); err != nil {
				a.close()
				return nil, err
			}
		}
	}
	batches, err := readAll[Batch](a, "batches")
	if err != nil {
		a.close()
		return nil, err
	}
	for _, b := range batches {
		stageInterrupted := false
		if b.ReadState == "running" || b.ReadState == "queued" {
			b.ReadState, b.ReadError = "interrupted", "读取已中断，点击标签重试"
			stageInterrupted = true
		}
		if b.QualityState == "running" || b.QualityState == "queued" {
			b.QualityState = "interrupted"
			b.QualityError = "检测已中断，点击标签重试"
			stageInterrupted = true
		}
		if stageInterrupted {
			if err := a.put("batches", b.ID, b); err != nil {
				a.close()
				return nil, err
			}
		}
		if b.State == "importing" {
			b.State, b.Message = "ready", "上次任务中断，已完成部分保留，可继续处理剩余文件"
			if len(b.Files) == 0 && len(b.Imported) > 0 {
				b.State = "imported"
			}
			if err := a.put("batches", b.ID, b); err != nil {
				a.close()
				return nil, err
			}
		}
	}
	return a, nil
}
func (a *app) checkStorage() error {
	if a.storageGuard != nil {
		return a.storageGuard()
	}
	return nil
}
func (a *app) close() {
	a.requestMu.Lock()
	a.closing = true
	a.requestMu.Unlock()
	a.requests.Wait()
	a.wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db != nil {
		_ = a.db.Close()
	}
	if a.dbAnchor != nil {
		_ = a.dbAnchor.Close()
	}
	if a.music != nil {
		_ = a.music.Close()
	}
	if a.artists != nil {
		_ = a.artists.Close()
	}
	if a.data != nil {
		_ = a.data.Close()
	}
}
func (a *app) put(bucket, id string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return a.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(bucket)).Put([]byte(id), raw) })
}
func get[T any](a *app, bucket, id string) (T, error) {
	var out T
	err := a.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(bucket)).Get([]byte(id))
		if raw == nil {
			return os.ErrNotExist
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}
func readAll[T any](a *app, bucket string) ([]T, error) {
	var out []T
	err := a.db.View(func(tx *bolt.Tx) error {
		var err error
		out, err = readAllTx[T](tx, bucket)
		return err
	})
	return out, err
}
func readAllTx[T any](tx *bolt.Tx, bucket string) ([]T, error) {
	out := []T{}
	err := tx.Bucket([]byte(bucket)).ForEach(func(_, v []byte) error {
		var x T
		if err := json.Unmarshal(v, &x); err != nil {
			return fmt.Errorf("读取 %s 索引失败：%w", bucket, err)
		}
		out = append(out, x)
		return nil
	})
	return out, err
}
func libraryArtist(t Track) string {
	if t.Tags.AlbumArtist != "" {
		return t.Tags.AlbumArtist
	}
	if t.Tags.Artist != "" {
		return t.Tags.Artist
	}
	return "未知歌手"
}
