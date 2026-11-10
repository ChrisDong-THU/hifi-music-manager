package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func ensureDir(root *os.Root, p string) error {
	return ensureDirectories(root, p, nil)
}

func ensureDirectories(root *os.Root, p string, created *[]string) error {
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
			if created != nil {
				*created = append(*created, cur)
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
	s = limitComponent(s)
	if s == "" {
		s = "未知"
	}
	switch strings.ToUpper(strings.Split(s, ".")[0]) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		s = "_" + s
	case "COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³":
		s = "_" + s
	}
	// Escaping a device name also consumes space in the component budget.
	return limitComponent(s)
}

func limitComponent(s string) string {
	runes := []rune(strings.Trim(s, ". "))
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return limitNameBytes(string(runes), 200)
}

func limitNameBytes(s string, maxBytes int) string {
	if len(s) > maxBytes {
		for !utf8.RuneStart(s[maxBytes]) {
			maxBytes--
		}
		s = s[:maxBytes]
	}
	// Rune or byte truncation can expose a trailing dot/space even if the
	// original name was normalized. Windows treats those names as aliases.
	return strings.TrimRight(s, ". ")
}
func destinationAlbumDirectory(tags Tags) string {
	artist := albumCredit(tags)
	album := tags.Album
	if album == "" {
		album = "未分类"
	}
	return path.Join(component(artist), component(album))
}

func destinationFilename(t Track, tags Tags) string {
	title := tags.Title
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
	}
	var prefix string
	if disc := tagNumber(tags.Disc); disc > 0 {
		prefix = strconv.Itoa(disc) + "-"
	}
	if tags.Track != "" {
		n, _, _ := strings.Cut(tags.Track, "/")
		prefix += limitNameBytes(component(n), 32) + " - "
	}
	ext := strings.ToLower(filepath.Ext(t.Path))
	// Leave room in the complete basename for a collision suffix, in bytes.
	return prefix + limitNameBytes(component(title), 255-len(prefix)-len(ext)-len(" (1000)")) + ext
}
