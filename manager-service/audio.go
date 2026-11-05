package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	id3 "github.com/bogem/id3v2/v2"
	"github.com/dhowden/tag"
)

var audioExtensions = map[string]bool{".flac": true, ".mp3": true, ".m4a": true, ".ogg": true, ".opus": true, ".wav": true, ".aiff": true, ".aif": true}

func cleanRelative(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, ":") {
		return "", errors.New("文件路径无效")
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, ".") || strings.ContainsAny(s, "\x00\r\n") {
			return "", errors.New("不允许隐藏目录或越界路径")
		}
	}
	return p, nil
}
func regular(root *os.Root, p string) error {
	parts := strings.Split(filepath.ToSlash(p), "/")
	prefix := ""
	for i, part := range parts {
		prefix = filepath.Join(prefix, part)
		st, err := root.Lstat(prefix)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("不读取符号链接")
		}
		if i < len(parts)-1 && !st.IsDir() {
			return errors.New("路径包含普通文件")
		}
		if i == len(parts)-1 && !st.Mode().IsRegular() {
			return errors.New("不是普通文件")
		}
	}
	return nil
}
func digest(f io.Reader) (string, error) {
	h := sha256.New()
	_, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), err
}
func fileHash(root *os.Root, p string) (string, error) {
	if err := regular(root, p); err != nil {
		return "", err
	}
	f, err := root.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return digest(f)
}

type flacBlock struct {
	kind byte
	data []byte
}

type boundedMetadataReader struct {
	io.ReadSeeker
	remaining int64
}

func (r *boundedMetadataReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("音频标签超过读取上限")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.ReadSeeker.Read(p)
	r.remaining -= int64(n)
	return n, err
}

func flacHeader(f io.ReadSeeker) ([]flacBlock, int64, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return nil, 0, err
	}
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil || string(magic[:]) != "fLaC" {
		return nil, 0, errors.New("无效的 FLAC 文件")
	}
	blocks := []flacBlock{}
	total := 0
	for len(blocks) < 256 {
		var h [4]byte
		if _, err := io.ReadFull(f, h[:]); err != nil {
			return nil, 0, err
		}
		n := int(h[1])<<16 | int(h[2])<<8 | int(h[3])
		total += n
		if total > 64<<20 {
			return nil, 0, errors.New("FLAC 标签过大")
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(f, data); err != nil {
			return nil, 0, err
		}
		if len(blocks) == 0 && (h[0]&127 != 0 || n != 34) {
			return nil, 0, errors.New("FLAC 缺少 STREAMINFO")
		}
		blocks = append(blocks, flacBlock{h[0] & 127, data})
		if h[0]&128 != 0 {
			off, err := f.Seek(0, 1)
			return blocks, off, err
		}
	}
	return nil, 0, errors.New("FLAC 标签块过多")
}
func audioDigest(ctx context.Context, f *os.File, format string) (string, float64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	start, end := int64(0), st.Size()
	seconds := float64(0)
	switch format {
	case "FLAC":
		blocks, off, e := flacHeader(operationReadSeeker{ReadSeeker: f, ctx: ctx})
		if e != nil {
			return "", 0, e
		}
		start = off
		v := binary.BigEndian.Uint64(blocks[0].data[10:18])
		rate := v >> 44
		samples := v & 0xfffffffff
		if rate > 0 {
			seconds = float64(samples) / float64(rate)
		}
	case "MP3":
		var h [10]byte
		_, _ = f.ReadAt(h[:], 0)
		if string(h[:3]) == "ID3" {
			for _, b := range h[6:10] {
				if b&128 != 0 {
					return "", 0, errors.New("无效 ID3 长度")
				}
			}
			start = 10 + int64(h[6])<<21 + int64(h[7])<<14 + int64(h[8])<<7 + int64(h[9])
			if h[3] == 4 && h[5]&16 != 0 {
				start += 10
			}
		}
		var tail [3]byte
		if end >= 128 {
			_, _ = f.ReadAt(tail[:], end-128)
			if string(tail[:]) == "TAG" {
				end -= 128
			}
		}
		if start+4 > end {
			return "", 0, errors.New("MP3 缺少音频帧")
		}
		var frame [4]byte
		_, _ = f.ReadAt(frame[:], start)
		if frame[0] != 255 || frame[1]&224 != 224 {
			return "", 0, errors.New("MP3 音频帧无效")
		}
		// Estimate VBR duration conservatively; the original stream is never decoded.
		ver := (frame[1] >> 3) & 3
		layer := (frame[1] >> 1) & 3
		index := frame[2] >> 4
		if layer == 1 && index > 0 && index < 15 {
			rates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
			if ver != 3 {
				rates = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
			}
			seconds = float64(end-start) * 8 / float64(rates[index]*1000)
		}
	}
	if start >= end {
		return "", 0, errors.New("文件没有音频数据")
	}
	_, err = f.Seek(start, 0)
	if err != nil {
		return "", 0, err
	}
	hash, err := digest(operationReader{ctx: ctx, r: io.LimitReader(f, end-start)})
	return hash, seconds, err
}

// Metadata and both content hashes share cancellation. A stopped request or
// detached disk must not continue reading a multi-gigabyte file in the background.
func inspectContext(ctx context.Context, root *os.Root, p string) (Track, error) {
	if err := ctx.Err(); err != nil {
		return Track{}, err
	}
	if err := regular(root, p); err != nil {
		return Track{}, err
	}
	f, err := root.Open(p)
	if err != nil {
		return Track{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Track{}, err
	}
	ext := strings.ToLower(filepath.Ext(p))
	format := strings.ToUpper(strings.TrimPrefix(ext, "."))
	if !audioExtensions[ext] {
		return Track{}, errors.New("不支持的音频格式")
	}
	t := Track{ID: uid(), Path: filepath.ToSlash(p), Format: format, Size: st.Size(), Editable: format == "FLAC" || format == "MP3", Added: now()}
	t.Modified = st.ModTime().UnixNano()
	if format == "FLAC" {
		if _, _, err := flacHeader(operationReadSeeker{ReadSeeker: f, ctx: ctx}); err != nil {
			return Track{}, err
		}
		_, _ = f.Seek(0, 0)
	}
	if format == "MP3" {
		var h [10]byte
		_, _ = f.ReadAt(h[:], 0)
		if string(h[:3]) == "ID3" {
			size := int64(h[6])<<21 | int64(h[7])<<14 | int64(h[8])<<7 | int64(h[9])
			if size > 32<<20 {
				return Track{}, errors.New("ID3 标签超过 32 MiB")
			}
		}
	}
	m, e := tag.ReadFrom(&boundedMetadataReader{ReadSeeker: operationReadSeeker{ReadSeeker: f, ctx: ctx}, remaining: 64 << 20})
	if e == nil {
		t.Tags = Tags{Title: m.Title(), Artist: m.Artist(), Album: m.Album(), AlbumArtist: m.AlbumArtist(), Genre: m.Genre(), Lyrics: m.Lyrics()}
		if y := m.Year(); y > 0 {
			t.Tags.Year = strconv.Itoa(y)
		}
		if n, _ := m.Track(); n > 0 {
			t.Tags.Track = strconv.Itoa(n)
			if _, total := m.Track(); total > 0 {
				t.Tags.Track += "/" + strconv.Itoa(total)
			}
		}
		if n, _ := m.Disc(); n > 0 {
			t.Tags.Disc = strconv.Itoa(n)
			if _, total := m.Disc(); total > 0 {
				t.Tags.Disc += "/" + strconv.Itoa(total)
			}
		}
	} else if format != "MP3" && format != "WAV" {
		return Track{}, fmt.Errorf("音频标签无法读取：%w", e)
	}
	if format == "WAV" {
		var h [12]byte
		_, _ = f.ReadAt(h[:], 0)
		if string(h[:4]) != "RIFF" || string(h[8:]) != "WAVE" {
			return Track{}, errors.New("无效 WAV 文件")
		}
	}
	if _, err = f.Seek(0, 0); err != nil {
		return Track{}, err
	}
	t.Hash, err = digest(operationReader{ctx: ctx, r: f})
	if err != nil {
		return Track{}, err
	}
	t.AudioHash, t.Duration, err = audioDigest(ctx, f, format)
	if err != nil {
		return Track{}, err
	}
	t.AudioInfo = readAudioInfo(f, format)
	if err := ctx.Err(); err != nil {
		return Track{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return Track{}, err
	}
	if after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) {
		return Track{}, errors.New("文件在读取期间发生变化，请重试")
	}
	return t, nil
}

func parseComments(data []byte) (string, []string, error) {
	r := bytes.NewReader(data)
	read := func() (string, error) {
		var n uint32
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return "", err
		}
		if uint64(n) > uint64(r.Len()) {
			return "", io.ErrUnexpectedEOF
		}
		b := make([]byte, int(n))
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	v, err := read()
	if err != nil {
		return "", nil, err
	}
	var n uint32
	if err = binary.Read(r, binary.LittleEndian, &n); err != nil || n > 100000 {
		return "", nil, errors.New("无效 Vorbis 标签数量")
	}
	comments := []string{}
	for i := uint32(0); i < n; i++ {
		s, e := read()
		if e != nil {
			return "", nil, e
		}
		comments = append(comments, s)
	}
	return v, comments, nil
}
func writeComments(data []byte, t Tags, masks ...map[string]string) ([]byte, error) {
	vendor, comments, err := parseComments(data)
	if err != nil {
		return nil, err
	}
	fields := map[string]string{"TITLE": t.Title, "ARTIST": t.Artist, "ALBUM": t.Album, "ALBUMARTIST": t.AlbumArtist, "GENRE": t.Genre, "DATE": t.Year, "TRACKNUMBER": t.Track, "DISCNUMBER": t.Disc, "LYRICS": t.Lyrics}
	if len(masks) > 0 {
		keys := map[string]string{"title": "TITLE", "artist": "ARTIST", "album": "ALBUM", "album_artist": "ALBUMARTIST", "genre": "GENRE", "year": "DATE", "track": "TRACKNUMBER", "disc": "DISCNUMBER", "lyrics": "LYRICS"}
		for key := range fields {
			selected := false
			for field := range masks[0] {
				if keys[field] == key {
					selected = true
					break
				}
			}
			if !selected {
				delete(fields, key)
			}
		}
	}
	kept := []string{}
	for _, c := range comments {
		key, _, _ := strings.Cut(c, "=")
		upper := strings.ToUpper(key)
		_, changingDate := fields["DATE"]
		_, changingArtist := fields["ALBUMARTIST"]
		if (upper == "YEAR" && changingDate) || (upper == "ALBUM_ARTIST" && changingArtist) {
			continue
		}
		if _, replace := fields[upper]; !replace {
			kept = append(kept, c)
		}
	}
	for _, key := range []string{"TITLE", "ARTIST", "ALBUM", "ALBUMARTIST", "GENRE", "DATE", "TRACKNUMBER", "DISCNUMBER", "LYRICS"} {
		if value, ok := fields[key]; ok && value != "" {
			kept = append(kept, key+"="+fields[key])
		}
	}
	var out bytes.Buffer
	write := func(s string) { _ = binary.Write(&out, binary.LittleEndian, uint32(len(s))); out.WriteString(s) }
	write(vendor)
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(kept)))
	for _, c := range kept {
		write(c)
	}
	return out.Bytes(), nil
}
func rewriteFLAC(root *os.Root, p string, t Tags, cover *albumCover, masks ...map[string]string) error {
	f, err := root.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	blocks, _, err := flacHeader(f)
	if err != nil {
		return err
	}
	found := false
	for i, b := range blocks {
		if b.kind == 4 {
			if found {
				return errors.New("存在多个 Vorbis 标签块，请先修复文件")
			}
			blocks[i].data, err = writeComments(b.data, t, masks...)
			if err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		var empty bytes.Buffer
		_ = binary.Write(&empty, binary.LittleEndian, uint32(4))
		empty.WriteString("HiFi")
		_ = binary.Write(&empty, binary.LittleEndian, uint32(0))
		d, e := writeComments(empty.Bytes(), t, masks...)
		if e != nil {
			return e
		}
		blocks = append(blocks, flacBlock{4, d})
	}
	if cover != nil {
		kept := []flacBlock{blocks[0], {kind: 6, data: cover.flacPicture()}}
		for _, b := range blocks[1:] {
			if b.kind == 6 && len(b.data) >= 4 && binary.BigEndian.Uint32(b.data[:4]) == 3 {
				continue
			}
			kept = append(kept, b)
		}
		blocks = kept
	}
	tmp := p + ".tag-" + uid()
	out, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = out.Write([]byte("fLaC"))
	for i, b := range blocks {
		if err != nil {
			break
		}
		if len(b.data) > 0xffffff {
			err = errors.New("标签块过大")
			break
		}
		h := []byte{b.kind, byte(len(b.data) >> 16), byte(len(b.data) >> 8), byte(len(b.data))}
		if i == len(blocks)-1 {
			h[0] |= 128
		}
		_, err = out.Write(h)
		if err == nil {
			_, err = out.Write(b.data)
		}
	}
	if err == nil {
		_, err = io.Copy(out, f)
	}
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	_ = f.Close()
	return root.Rename(tmp, p)
}
func rewriteMP3(root *os.Root, p string, t Tags, cover *albumCover, masks ...map[string]string) error {
	// id3v2 is restricted to a private copy addressed through this root. Its
	// Save implementation is never allowed to open a user supplied absolute path.
	in, err := root.Open(p)
	if err != nil {
		return err
	}
	defer in.Close()
	v, err := id3.ParseReader(in, id3.Options{Parse: true})
	if err != nil {
		return err
	}
	v.SetDefaultEncoding(id3.EncodingUTF8)
	wanted := func(k string) bool {
		if len(masks) == 0 {
			return true
		}
		_, ok := masks[0][k]
		return ok
	}
	if wanted("title") {
		v.SetTitle(t.Title)
	}
	if wanted("artist") {
		v.SetArtist(t.Artist)
	}
	if wanted("album") {
		v.SetAlbum(t.Album)
	}
	if wanted("genre") {
		v.SetGenre(t.Genre)
	}
	if wanted("year") {
		v.DeleteFrames("TYER")
		v.DeleteFrames("TDRC")
		if t.Year != "" {
			v.SetYear(t.Year)
		}
	}
	for field, val := range map[string]string{"album_artist": t.AlbumArtist, "track": t.Track, "disc": t.Disc} {
		if !wanted(field) {
			continue
		}
		key := map[string]string{"album_artist": "TPE2", "track": "TRCK", "disc": "TPOS"}[field]
		v.DeleteFrames(key)
		if val != "" {
			v.AddTextFrame(key, id3.EncodingUTF8, val)
		}
	}
	if wanted("lyrics") {
		v.DeleteFrames("USLT")
		if t.Lyrics != "" {
			v.AddUnsynchronisedLyricsFrame(id3.UnsynchronisedLyricsFrame{Encoding: id3.EncodingUTF8, Language: "und", ContentDescriptor: "", Lyrics: t.Lyrics})
		}
	}
	if cover != nil {
		pictures := v.GetFrames("APIC")
		v.DeleteFrames("APIC")
		v.AddAttachedPicture(id3.PictureFrame{Encoding: id3.EncodingUTF8, MimeType: cover.MIME, PictureType: 3, Description: "Front cover", Picture: cover.Data})
		for _, frame := range pictures {
			if picture, ok := frame.(id3.PictureFrame); ok && picture.PictureType != 3 {
				v.AddAttachedPicture(picture)
			}
		}
	}
	var h [10]byte
	_, _ = in.ReadAt(h[:], 0)
	offset := int64(0)
	if string(h[:3]) == "ID3" {
		offset = 10 + int64(h[6])<<21 + int64(h[7])<<14 + int64(h[8])<<7 + int64(h[9])
		if h[3] == 4 && h[5]&16 != 0 {
			offset += 10
		}
	}
	if _, err = in.Seek(offset, 0); err != nil {
		return err
	}
	tmp := p + ".tag-" + uid()
	out, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = v.WriteTo(out)
	if err == nil {
		_, err = io.Copy(out, in)
	}
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err == nil {
		err = ce
	}
	_ = in.Close()
	if err != nil {
		return err
	}
	return root.Rename(tmp, p)
}
func writeTags(root *os.Root, p string, t Tags, cover *albumCover, masks ...map[string]string) error {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".flac":
		return rewriteFLAC(root, p, t, cover, masks...)
	case ".mp3":
		return rewriteMP3(root, p, t, cover, masks...)
	default:
		return errors.New("当前仅支持写入 FLAC 和 MP3 标签与封面；其他格式可原样入库")
	}
}

func patchTags(t Tags, patch map[string]string) (Tags, error) {
	fields := map[string]*string{"title": &t.Title, "artist": &t.Artist, "album": &t.Album, "album_artist": &t.AlbumArtist, "genre": &t.Genre, "year": &t.Year, "track": &t.Track, "disc": &t.Disc, "lyrics": &t.Lyrics}
	if len(patch) == 0 {
		return t, errors.New("没有要修改的字段")
	}
	for k, v := range patch {
		ptr, ok := fields[k]
		if !ok {
			return t, fmt.Errorf("未知标签字段：%s", k)
		}
		if len(v) > 64000 || strings.ContainsRune(v, 0) {
			return t, errors.New("标签过长或包含无效字符")
		}
		if k != "lyrics" {
			v = strings.TrimSpace(v)
		}
		if (k == "track" || k == "disc") && v != "" {
			parts := strings.Split(v, "/")
			if len(parts) > 2 {
				return t, errors.New("曲序和碟号应为数字或 数字/总数")
			}
			for _, n := range parts {
				if strings.Trim(n, "0123456789") != "" {
					return t, errors.New("曲序和碟号应为数字或 数字/总数")
				}
				x, e := strconv.Atoi(n)
				if e != nil || x < 0 || x > 9999 {
					return t, errors.New("曲序和碟号应为数字或 数字/总数")
				}
			}
		}
		if k == "year" && v != "" {
			if len(v) != 4 || strings.Trim(v, "0123456789") != "" {
				return t, errors.New("年份应为四位数字")
			}
			if year, err := strconv.Atoi(v); err != nil || year == 0 {
				return t, errors.New("年份应为四位数字")
			}
		}
		*ptr = v
	}
	return t, nil
}

func tagValues(t Tags) map[string]string {
	return map[string]string{
		"title": t.Title, "artist": t.Artist, "album": t.Album,
		"album_artist": t.AlbumArtist, "genre": t.Genre, "year": t.Year,
		"track": t.Track, "disc": t.Disc, "lyrics": t.Lyrics,
	}
}

func tagDiff(before, after Tags) map[string]string {
	a, b := tagValues(before), tagValues(after)
	out := map[string]string{}
	for k, v := range b {
		if a[k] != v {
			out[k] = v
		}
	}
	return out
}
