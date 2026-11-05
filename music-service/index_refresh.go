package main

import (
	"context"
	"log"
	"time"
)

// The watcher owns this worker. Slow vendor rescans never stall USB supervision;
// detaching a disk cancels and joins its worker before another player is started.
type indexRefresh struct {
	request func(context.Context) error
	cancel  context.CancelFunc
	done    chan struct{}
}

func (r *indexRefresh) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done
		r.cancel = nil
	}
}

func (r *indexRefresh) start(parent context.Context) {
	r.stop()
	ctx, cancel := context.WithCancel(parent)
	r.cancel, r.done = cancel, make(chan struct{})
	go func() {
		defer close(r.done)
		lastError := ""
		for ctx.Err() == nil {
			err := r.request(ctx)
			if err == nil {
				return
			}
			if ctx.Err() != nil {
				return
			}
			if err.Error() != lastError {
				log.Printf("MinimServer index refresh pending: %v", err)
				lastError = err.Error()
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}
