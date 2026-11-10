package main

import "errors"

// Resolve IDs against the current draft before changing files or metadata.
func selectedDraftFiles(b Batch, ids []string) (map[string]bool, error) {
	if len(ids) == 0 || len(ids) > 300 {
		return nil, errors.New("请选择 1 至 300 首歌曲")
	}
	available := make(map[string]bool, len(b.Files))
	for _, file := range b.Files {
		available[file.ID] = true
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !available[id] || selected[id] {
			return nil, errors.New("所选歌曲与当前草稿不匹配，请刷新后重试")
		}
		selected[id] = true
	}
	return selected, nil
}

// Keep saved tags, quality results and artwork aligned with remaining sources.
func draftWithoutFiles(b Batch, removed map[string]bool) Batch {
	next := b
	next.Files = nil
	for _, file := range b.Files {
		if !removed[file.ID] {
			next.Files = append(next.Files, file)
		}
	}
	next.Candidates = nil
	for _, candidate := range b.Candidates {
		if len(candidate.Tags) != len(b.Files) {
			continue
		}
		tags := make([]Tags, 0, len(next.Files))
		for i, file := range b.Files {
			if !removed[file.ID] {
				tags = append(tags, candidate.Tags[i])
			}
		}
		candidate.Tags = tags
		next.Candidates = append(next.Candidates, candidate)
	}
	next.Quality = qualityForFiles(b.Quality, next.Files)
	return next
}
