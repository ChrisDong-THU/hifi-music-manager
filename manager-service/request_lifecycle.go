package main

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// A request leases the selected app, rather than holding the controller lock
// through an upload or audio stream. Closing an app first refuses new leases.
func (a *app) beginRequest(mutation bool) bool {
	a.requestMu.Lock()
	defer a.requestMu.Unlock()
	if a.closing {
		return false
	}
	a.requests.Add(1)
	if mutation {
		a.mutations.Add(1)
	}
	return true
}

func (a *app) serveRequest(w http.ResponseWriter, r *http.Request, handler http.Handler, mutation bool) {
	defer a.requests.Done()
	if mutation {
		defer a.mutations.Add(-1)
	}
	if err := a.checkStorage(); err != nil {
		problem(w, 503, err)
		return
	}
	if a.ctx != nil {
		ctx, cancel := context.WithCancel(r.Context())
		rc := http.NewResponseController(w)
		interrupted := make(chan struct{})
		stop := context.AfterFunc(a.ctx, func() {
			defer close(interrupted)
			cancel()
			// Cancellation alone does not interrupt a blocked network read/write.
			_ = rc.SetReadDeadline(time.Now())
			_ = rc.SetWriteDeadline(time.Now())
		})
		defer func() {
			if !stop() {
				<-interrupted
				_ = rc.SetReadDeadline(time.Time{})
				_ = rc.SetWriteDeadline(time.Time{})
			}
			cancel()
		}()
		r = r.WithContext(ctx)
	}
	handler.ServeHTTP(w, r)
}

func (a *app) trackRequests(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutation := r.Method != "GET" && r.Method != "HEAD"
		if !a.beginRequest(mutation) {
			problem(w, 503, errors.New("数据盘正在切换，请稍后重试"))
			return
		}
		a.serveRequest(w, r, handler, mutation)
	})
}
