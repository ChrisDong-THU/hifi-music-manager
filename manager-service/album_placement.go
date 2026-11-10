package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// A match identifies an existing physical album location as well as its
// normalized title and album artist. The catalog's existing keys stay intact.
type albumPlacement struct {
	Key          string `json:"key"`
	Title        string `json:"title"`
	Artist       string `json:"artist"`
	Year         string `json:"year"`
	Count        int    `json:"count"`
	Location     string `json:"location"`
	YearConflict bool   `json:"year_conflict,omitempty"`
}

func (r *albumRelease) placement() albumPlacement {
	return albumPlacement{Key: r.matchKey, Title: r.Title, Artist: r.Artist,
		Year: r.Year, Count: r.Count, Location: r.location, YearConflict: r.yearConflict}
}

func albumIdentity(title, artist string) string {
	return identityText(title) + "\x00" + artistCreditIdentity(artist)
}

func albumPlacementKey(identity, location string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity+"\x00"+location)))
}

func discDirectoryNumber(name string) int {
	match := discFolder.FindStringSubmatch(name)
	if match == nil {
		return 0
	}
	number, _ := strconv.Atoi(match[1])
	if number > 9999 {
		return 0
	}
	return number
}

func editionYear(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 4 && strings.ContainsRune("-/.", rune(value[4])) &&
		strings.Trim(value[:4], "0123456789") == "" && albumYear(value) != 0 {
		return value[:4]
	}
	return value
}

func requiresAlbumChoice(matches []*albumRelease, year string) bool {
	if len(matches) != 1 {
		return len(matches) > 1
	}
	match := matches[0]
	year = editionYear(year)
	return match.yearConflict || year != "" && match.Year != "" && match.Year != year
}

func chooseAlbum(matches []*albumRelease, key, year string) (*albumRelease, error) {
	if key == "new" {
		if year = editionYear(year); len(year) != 4 || strings.Trim(year, "0123456789") != "" || year == "0000" {
			return nil, errors.New("独立版本需要明确的发行年份")
		}
		return nil, nil
	}
	if key != "" {
		for _, match := range matches {
			if match.matchKey == key || slices.Contains(match.previousMatchKeys, key) {
				return match, nil
			}
		}
		return nil, errors.New("已选择的专辑位置已变更，请重新选择")
	}
	if requiresAlbumChoice(matches, year) {
		return nil, errors.New("发现同名同歌手专辑的多个位置或发行年份冲突，请在草稿编辑中选择专辑位置后保存；混合草稿可先按专辑整理")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return nil, nil
}

func (match *albumRelease) canonicalTags(tags Tags) Tags {
	tags.Album = match.Title
	// Keep the existing album credit spelling. Track artist is independent.
	if tags.AlbumArtist != "" || tags.Artist != match.Artist {
		tags.AlbumArtist = match.Artist
	}
	if match.Year != "" && editionYear(tags.Year) != match.Year {
		tags.Year = match.Year
	}
	return tags
}

// A plan is built before publishing any track and reserves its chosen album
// directories for the whole job, including names that sanitize to the same path.
type importPlacementPlan struct {
	catalog *catalog
	root    *os.Root
	new     map[string]string
	used    map[string]bool
	discs   map[*albumRelease]releaseDirectories
	years   map[string]map[string]bool
}

func (p *importPlacementPlan) noteYear(tags Tags) {
	if year := editionYear(tags.Year); year != "" {
		identity := albumIdentity(tags.Album, albumCredit(tags))
		if p.years[identity] == nil {
			p.years[identity] = map[string]bool{}
		}
		p.years[identity][year] = true
	}
}

func (p *importPlacementPlan) match(tags Tags, key string) (*albumRelease, string, error) {
	identity := albumIdentity(tags.Album, albumCredit(tags))
	releaseYear := editionYear(tags.Year)
	if releaseYear == "" && len(p.years[identity]) == 1 {
		for year := range p.years[identity] {
			releaseYear = year
		}
	}
	matches := p.catalog.byIdentity[identity]
	hasIdentity := strings.TrimSpace(tags.Album) != "" && strings.TrimSpace(albumCredit(tags)) != ""
	if hasIdentity {
		if key == "" && len(matches) > 0 && len(p.years[identity]) > 1 {
			return nil, "", errors.New("所选曲目包含同名专辑的多个发行年份，请分批导入或明确选择目标专辑")
		}
		release, err := chooseAlbum(matches, key, releaseYear)
		return release, releaseYear, err
	}
	if key != "" {
		return nil, "", errors.New("选择专辑位置前请填写专辑名和专辑歌手")
	}
	if len(matches) == 1 {
		return matches[0], releaseYear, nil
	}
	return nil, releaseYear, nil
}

func (p *importPlacementPlan) place(source Track, tags Tags, key string) (Tags, string, *albumRelease, error) {
	match, releaseYear, err := p.match(tags, key)
	if err != nil {
		return Tags{}, "", nil, err
	}
	disc := tagNumber(tags.Disc)
	if disc == 0 {
		disc = albumDirectoryDisc(path.Dir(source.Path), tags.Album)
		if disc > 0 && source.Editable {
			tags.Disc = strconv.Itoa(disc)
		}
	}
	if match != nil {
		if strings.TrimSpace(tags.Album) != "" && strings.TrimSpace(albumCredit(tags)) != "" {
			canonical := match.canonicalTags(tags)
			if source.Editable {
				tags = canonical
			} else if albumKey(Track{Tags: tags}) != albumKey(Track{Tags: canonical}) ||
				tags.Year != "" && canonical.Year != tags.Year {
				return Tags{}, "", nil, errors.New("此音频格式仅能原样导入，标签与目标专辑不一致")
			}
		}
		dir, err := p.existingDirectory(match, disc)
		if err != nil {
			return Tags{}, "", nil, err
		}
		if disc == 0 && source.Editable {
			if inferred := albumDirectoryDisc(dir, tags.Album); inferred > 0 {
				tags.Disc = strconv.Itoa(inferred)
			}
		}
		return tags, path.Join(dir, destinationFilename(source, tags)), match, nil
	}
	identity := albumIdentity(tags.Album, albumCredit(tags))
	if releaseYear == "" && len(p.years[identity]) > 1 {
		return Tags{}, "", nil, errors.New("所选曲目包含同名专辑的多个发行年份，请补齐年份或分批导入")
	}
	release := identity + "\x00" + releaseYear
	dir, ok := p.new[release]
	if !ok {
		base := destinationAlbumDirectory(tags)
		var err error
		dir, err = p.availableDirectory(base, release)
		if err != nil {
			return Tags{}, "", nil, err
		}
		p.new[release] = dir
		p.used[identityText(dir)] = true
	}
	return tags, path.Join(dir, destinationFilename(source, tags)), nil, nil
}

type releaseDirectories struct {
	discs    map[int][]string
	present  map[string]bool
	hasTrack bool
	numbered bool
	conflict bool
}

func (p *importPlacementPlan) existingDirectory(release *albumRelease, disc int) (string, error) {
	layout, ready := p.discs[release]
	if !ready {
		layout = releaseDirectories{discs: map[int][]string{}, present: map[string]bool{}}
		for _, pos := range release.members {
			track := p.catalog.tracks[pos]
			name := track.Path
			if local, err := cleanRelative(name); err != nil || local != name {
				continue
			}
			dir := path.Dir(name)
			if !layout.present[dir] {
				// Verify a surviving indexed track in each directory before
				// using it. A replacement file can occupy the same path.
				if regular(p.root, name) == nil {
					info, err := p.root.Lstat(filepath.FromSlash(name))
					layout.present[dir] = err == nil && info.Mode().IsRegular() &&
						(track.Modified == 0 || info.Size() == track.Size && info.ModTime().UnixNano() == track.Modified)
				} else {
					layout.present[dir] = false
				}
				layout.hasTrack = layout.hasTrack || layout.present[dir]
			}
			number := tagNumber(track.Tags.Disc)
			if fromDir := albumDirectoryDisc(dir, track.Tags.Album); fromDir != 0 {
				layout.numbered = true
				layout.conflict = layout.conflict || number != 0 && number != fromDir
				number = fromDir
			}
			if !slices.Contains(layout.discs[number], dir) {
				layout.discs[number] = append(layout.discs[number], dir)
			}
		}
		if p.discs == nil {
			p.discs = map[*albumRelease]releaseDirectories{}
		}
		p.discs[release] = layout
	}
	if !layout.hasTrack {
		return "", errors.New("现有专辑文件位置已变更，请重新扫描曲库后导入")
	}
	if layout.conflict {
		return "", errors.New("现有曲目的碟号与分碟目录不一致，请核对后导入")
	}
	var dir string
	switch {
	case !layout.numbered:
		dir = release.location
	case disc == 0:
		if len(layout.present) != 1 {
			return "", errors.New("此专辑有多个曲目目录，请先填写所选曲目的碟号")
		}
		for name := range layout.present {
			dir = name
		}
	default:
		switch dirs := layout.discs[disc]; len(dirs) {
		case 0:
			if len(layout.discs[0]) > 0 {
				return "", errors.New("专辑根目录中有未编号曲目，请先核对曲库中的碟号")
			}
			candidate := path.Join(release.location, "CD"+strconv.Itoa(disc))
			if _, err := p.root.Lstat(filepath.FromSlash(candidate)); errors.Is(err, os.ErrNotExist) {
				return candidate, nil
			} else if err != nil {
				return "", err
			}
			return "", errors.New("目标碟目录已存在，请重新扫描曲库并核对目录")
		case 1:
			dir = dirs[0]
		default:
			return "", errors.New("同一碟号已分布在多个目录，请先核对曲库中的碟号和目录")
		}
	}
	if !layout.present[dir] {
		return "", errors.New("现有专辑文件位置已变更，请重新扫描曲库后导入")
	}
	return dir, nil
}

func (p *importPlacementPlan) availableDirectory(base, identity string) (string, error) {
	if p.used == nil {
		p.used = map[string]bool{}
		for _, track := range p.catalog.tracks {
			for dir := path.Dir(track.Path); dir != "." && dir != "/"; dir = path.Dir(dir) {
				p.used[identityText(dir)] = true
			}
		}
	}
	sum := sha256.Sum256([]byte(identity))
	for attempt := 0; attempt < 100; attempt++ {
		candidate := base
		if attempt == 1 {
			candidate = fmt.Sprintf("%s [%x]", base, sum[:4])
		} else if attempt > 1 {
			candidate = fmt.Sprintf("%s [%x-%d]", base, sum[:4], attempt)
		}
		folded := identityText(candidate)
		if p.used[folded] {
			continue
		}
		if _, err := p.root.Lstat(filepath.FromSlash(candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("无法为新专辑分配独立目录")
}
