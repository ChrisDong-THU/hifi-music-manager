package main

import (
	"context"
	"encoding/json"
	"time"

	bolt "go.etcd.io/bbolt"
)

type playbackSync struct {
	Requested uint64 `json:"requested"`
	Delivered uint64 `json:"delivered"`
	Error     string `json:"error,omitempty"`
}

const playbackRetryMessage = "播放索引刷新暂未成功，将自动重试"

func (a *app) playbackSyncState() playbackSync {
	state, _ := get[playbackSync](a, "playback_sync", "state")
	return state
}

func (a *app) markPlaybackDirty() error {
	if err := a.checkStorage(); err != nil {
		return err
	}
	err := a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("playback_sync"))
		var state playbackSync
		if raw := bucket.Get([]byte("state")); raw != nil {
			if err := json.Unmarshal(raw, &state); err != nil {
				return err
			}
		}
		state.Requested++
		raw, err := marshal(state)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("state"), raw)
	})
	if err == nil && a.syncWake != nil {
		select {
		case a.syncWake <- struct{}{}:
		default:
		}
	}
	return err
}

func (a *app) syncPlayback(ctx context.Context) error {
	state := a.playbackSyncState()
	if state.Requested <= state.Delivered || a.rescan == nil {
		return nil
	}
	if err := a.checkStorage(); err != nil {
		return err
	}
	err := a.rescan(ctx)
	// A retry with the same failure has no new persistent information. Avoid
	// advancing the DB revision and invalidating every client's cached library.
	if err != nil && state.Error == playbackRetryMessage {
		return err
	}
	commitErr := a.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("playback_sync"))
		var latest playbackSync
		if raw := bucket.Get([]byte("state")); raw != nil {
			if e := json.Unmarshal(raw, &latest); e != nil {
				return e
			}
		}
		if err != nil {
			latest.Error = playbackRetryMessage
		} else {
			latest.Delivered = state.Requested
			latest.Error = ""
		}
		raw, e := marshal(latest)
		if e != nil {
			return e
		}
		return bucket.Put([]byte("state"), raw)
	})
	if commitErr != nil {
		return commitErr
	}
	return err
}

func (a *app) startPlaybackSync(ctx context.Context) {
	if a.rescan == nil {
		return
	}
	a.syncWake = make(chan struct{}, 1)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-a.syncWake:
			case <-timer.C:
			}
			// Coalesce writes without holding the resource/DB mutation lock.
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			_ = a.syncPlayback(ctx)
			timer.Reset(5 * time.Second)
		}
	}()
}
