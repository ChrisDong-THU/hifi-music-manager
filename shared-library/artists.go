// Package library defines the public resource format shared by independent services.
// It contains no database, service client or playback index.
package library

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const MaxPicture = 10 << 20

type Artist struct {
	Name      string `json:"name"`
	Biography string `json:"biography"`
	ImageHash string `json:"image_hash,omitempty"`
	ImageType string `json:"image_type,omitempty"`
}

func ArtistID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}
func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func directory(root *os.Root, path string, create bool) error {
	current := ""
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := root.Mkdir(current, 0755); err != nil {
				return err
			}
			info, err = root.Lstat(current)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("resource directory cannot be a symlink")
		}
	}
	return nil
}

func read(root *os.Root, path string, limit int64) ([]byte, error) {
	if err := directory(root, filepath.Dir(path), false); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid resource file")
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > int(limit) {
		return nil, errors.New("oversized resource")
	}
	return data, err
}

func LoadArtist(root *os.Root, name string) (Artist, error) {
	var artist Artist
	data, err := read(root, filepath.Join(ArtistID(name), "profile.json"), 32<<10)
	if err != nil {
		return artist, err
	}
	err = json.Unmarshal(data, &artist)
	if err == nil && artist.Name != name {
		err = errors.New("artist identity mismatch")
	}
	return artist, err
}

func Picture(root *os.Root, artist Artist) ([]byte, error) {
	if len(artist.ImageHash) != 64 || (artist.ImageType != "image/jpeg" && artist.ImageType != "image/png") {
		return nil, os.ErrNotExist
	}
	if _, err := hex.DecodeString(artist.ImageHash); err != nil {
		return nil, err
	}
	for _, extension := range []string{".jpg", ".png"} {
		data, err := read(root, filepath.Join(ArtistID(artist.Name), artist.ImageHash+extension), MaxPicture)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err == nil && Hash(data) != artist.ImageHash {
			err = errors.New("artist image checksum mismatch")
		}
		return data, err
	}
	return nil, os.ErrNotExist
}

func atomic(root *os.Root, path string, data []byte) error {
	return atomicMode(root, path, data, 0644)
}

func atomicMode(root *os.Root, path string, data []byte, mode os.FileMode) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(path), ".pending-"+hex.EncodeToString(nonce[:]))
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer root.Remove(temp)
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
	return root.Rename(temp, path)
}

// SaveArtist commits the profile last. Readers see either complete old or new
// resources. Immutable picture names also invalidate control-point caches.
func SaveArtist(root *os.Root, artist Artist, picture []byte) (Artist, error) {
	path := ArtistID(artist.Name)
	if err := directory(root, path, true); err != nil {
		return artist, err
	}
	previous, err := LoadArtist(root, artist.Name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return artist, err
	}
	if picture != nil {
		if len(picture) > MaxPicture {
			return artist, errors.New("picture too large")
		}
		artist.ImageHash = Hash(picture)
		extension := ".jpg"
		if artist.ImageType == "image/png" {
			extension = ".png"
		} else if artist.ImageType != "image/jpeg" {
			return artist, errors.New("unsupported artist image type")
		}
		if err := atomic(root, filepath.Join(path, artist.ImageHash+extension), picture); err != nil {
			return artist, err
		}
	}
	data, err := json.Marshal(artist)
	if err != nil {
		return artist, err
	}
	if err := atomic(root, filepath.Join(path, "profile.json"), data); err != nil {
		return artist, err
	}
	revision := Revision(root) + 1
	if err := atomic(root, "revision", []byte(strconv.FormatUint(uint64(revision), 10))); err != nil {
		return artist, err
	}
	if previous.ImageHash != "" && previous.ImageHash != artist.ImageHash && len(previous.ImageHash) == 64 {
		if _, err := hex.DecodeString(previous.ImageHash); err != nil {
			return artist, err
		}
		for _, ext := range []string{".jpg", ".png"} {
			p := filepath.Join(path, previous.ImageHash+ext)
			info, err := root.Lstat(p)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return artist, err
			}
			if !info.Mode().IsRegular() {
				return artist, errors.New("invalid previous picture")
			}
			if err := root.Remove(p); err != nil {
				return artist, err
			}
		}
	}
	return artist, nil
}

func Revision(root *os.Root) uint32 {
	data, err := read(root, "revision", 32)
	if err != nil {
		return 0
	}
	value, _ := strconv.ParseUint(string(data), 10, 32)
	return uint32(value)
}
