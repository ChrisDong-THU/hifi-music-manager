package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

func (a *app) start(ctx context.Context) {
	a.ctx = ctx
	a.startPlaybackSync(ctx)
	a.startCleanup(ctx)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case w := <-a.queue:
				a.jobMu.Lock()
				a.jobs[w.id].State = "running"
				kind := a.jobs[w.id].Kind
				a.jobMu.Unlock()
				err := a.checkStorage()
				if err == nil {
					err = w.fn()
					if a.rescan != nil && updatesPlaybackLibrary(kind) && a.checkStorage() == nil {
						if refreshErr := a.markPlaybackDirty(); refreshErr != nil {
							err = errors.Join(err, refreshErr)
						}
					}
				}
				a.jobMu.Lock()
				j := a.jobs[w.id]
				j.State = "done"
				j.Message = "操作完成"
				if err != nil {
					j.State = "failed"
					j.Message = err.Error()
				}
				a.jobMu.Unlock()
			}
		}
	}()
	_, _ = a.submit("scan", func() error { return a.scan() })
}
func (a *app) submit(kind string, fn func() error) (string, error) {
	return a.submitForBatch(kind, "", fn)
}

func (a *app) submitForBatch(kind, batchID string, fn func() error) (string, error) {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	if kind == "scan" {
		for _, j := range a.jobs {
			if j.Kind == kind && (j.State == "queued" || j.State == "running") {
				return "", errors.New("正在扫描曲库，请等待完成")
			}
		}
	}
	if len(a.queue) == cap(a.queue) {
		return "", errors.New("任务队列已满，请稍后重试")
	}
	// Keep recent progress bounded. Durable history lives in the database.
	if len(a.jobs) > 80 {
		for id, j := range a.jobs {
			if j.State == "done" || j.State == "failed" {
				delete(a.jobs, id)
			}
		}
	}
	id := uid()
	a.jobs[id] = &task{ID: id, BatchID: batchID, Kind: kind, State: "queued", Message: "等待处理"}
	a.queue <- work{id, fn}
	return id, nil
}
func (a *app) taskSnapshot() []task {
	a.jobMu.Lock()
	out := []task{}
	for _, j := range a.jobs {
		out = append(out, *j)
	}
	a.jobMu.Unlock()
	// OS/disk reads and sorting must not hold the progress-update mutex.
	if a.externalTasks != nil {
		out = append(out, a.externalTasks()...)
	}
	if a.rescan != nil {
		sync := a.playbackSyncState()
		if sync.Requested > 0 {
			state, message := "done", "播放曲库已刷新"
			if sync.Requested > sync.Delivered {
				state, message = "running", "正在同步播放曲库"
			}
			if sync.Error != "" {
				state, message = "running", sync.Error
			}
			out = append(out, task{ID: fmt.Sprintf("playback-sync-%d", sync.Requested), Kind: "sync", State: state, Message: message})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
