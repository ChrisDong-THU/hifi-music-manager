package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ensureDir(root *os.Root, p string) error {
	if p == "." || p == "" {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(p), "/")
	cur := ""
	for _, s := range parts {
		cur = filepath.Join(cur, s)
		st, err := root.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(cur, 0700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("目标目录不安全")
		}
	}
	return nil
}

type operationReader struct {
	ctx context.Context
	r   io.Reader
}

type operationReadSeeker struct {
	io.ReadSeeker
	ctx context.Context
}

func (r operationReadSeeker) Read(p []byte) (int, error) {
	return (operationReader{ctx: r.ctx, r: r.ReadSeeker}).Read(p)
}

func (r operationReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReadSeeker.Seek(offset, whence)
}

func (a *app) operationContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *app) operationHash(root *os.Root, p string) (string, error) {
	ctx := a.operationContext()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := regular(root, p); err != nil {
		return "", err
	}
	f, err := root.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digest(operationReader{ctx: ctx, r: f})
}

func (r operationReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func (a *app) copyOperationFile(from *os.Root, src string, to *os.Root, dst string) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	ctx := a.operationContext()
	if err := copyFileContext(ctx, from, src, to, dst); err != nil {
		return err
	}
	return a.checkOperation()
}
func copyFileContext(ctx context.Context, from *os.Root, src string, to *os.Root, dst string) error {
	if err := regular(from, src); err != nil {
		return err
	}
	if err := ensureDir(to, filepath.Dir(dst)); err != nil {
		return err
	}
	in, err := from.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := to.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, operationReader{ctx: ctx, r: in})
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		_ = to.Remove(dst)
	}
	return err
}
func (a *app) current(t Track) error {
	if err := a.checkOperation(); err != nil {
		return err
	}
	hash, err := a.operationHash(a.music, t.Path)
	if err != nil {
		return err
	}
	if hash != t.Hash {
		return errors.New("文件已在服务外发生变化，请重新扫描后操作")
	}
	return nil
}
func component(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("<>:\"/\\|?*", r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(s, ". ")
	if len([]rune(s)) > 100 {
		s = string([]rune(s)[:100])
	}
	// Linux filename limits count bytes; keep room for extensions and collisions.
	for len(s) > 200 {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	if s == "" {
		s = "未知"
	}
	switch strings.ToUpper(strings.Split(s, ".")[0]) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = "_" + s
	}
	return s
}
func destination(t Track, tags Tags) string {
	artist := tags.AlbumArtist
	if artist == "" {
		artist = tags.Artist
	}
	album := tags.Album
	if album == "" {
		album = "未分类"
	}
	title := tags.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
	}
	name := component(title)
	if tags.Track != "" {
		n, _, _ := strings.Cut(tags.Track, "/")
		name = component(n) + " - " + name
	}
	return filepath.ToSlash(filepath.Join(component(artist), component(album), name+strings.ToLower(filepath.Ext(t.Path))))
}
