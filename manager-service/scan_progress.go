package main

import "net/http"

type libraryStats struct {
	Tracks   int     `json:"tracks"`
	Artists  int     `json:"artists"`
	Albums   int     `json:"albums"`
	Bytes    int64   `json:"bytes"`
	Duration float64 `json:"duration"`
}

type statsAccumulator struct {
	stats   libraryStats
	artists map[string]bool
	albums  map[albumGroup]bool
}

func newStatsAccumulator() *statsAccumulator {
	return &statsAccumulator{artists: map[string]bool{}, albums: map[albumGroup]bool{}}
}

func (s *statsAccumulator) add(t Track) {
	for _, name := range artistCreditNames(libraryArtist(t)) {
		s.artists[name] = true
	}
	s.albums[albumGroup{albumIdentity(t.Tags.Album, albumCredit(t.Tags)), albumLocation(t.Path, t.Tags)}] = true
	s.stats.Tracks++
	s.stats.Artists, s.stats.Albums = len(s.artists), len(s.albums)
	s.stats.Bytes += t.Size
	s.stats.Duration += t.Duration
}

type scanProgress struct {
	Stats libraryStats `json:"stats"`
	Done  int          `json:"done"`
	Total int          `json:"total"`
}

func (a *app) reportScanProgress(stats libraryStats, done, total int) {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	if a.scanning == nil {
		a.scanning = &scanProgress{}
	}
	*a.scanning = scanProgress{Stats: stats, Done: done, Total: total}
	for _, j := range a.jobs {
		if j.Kind == "scan" && j.State == "running" {
			j.Done, j.Total = done, total
		}
	}
}

// Frequent home-card updates read only a small runtime snapshot, without
// decoding the committed library, drafts or operation history on each poll.
func (a *app) scanState(w http.ResponseWriter, r *http.Request) {
	a.jobMu.Lock()
	var snapshot *scanProgress
	if a.scanning != nil {
		copy := *a.scanning
		snapshot = &copy
	}
	active := snapshot != nil
	for _, j := range a.jobs {
		active = active || (j.Kind == "scan" && (j.State == "queued" || j.State == "running"))
	}
	a.jobMu.Unlock()
	respond(w, struct {
		Active bool `json:"active"`
		*scanProgress
	}{Active: active, scanProgress: snapshot})
}
