package main

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// Limit inactivity rather than total transfer time: a large, steadily arriving
// upload may take hours, but a disconnected client must release the file lock.
type idleRequestBody struct {
	io.ReadCloser
	controller *http.ResponseController
	idle       time.Duration
}

func (b *idleRequestBody) Read(p []byte) (int, error) {
	if err := b.controller.SetReadDeadline(time.Now().Add(b.idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return b.ReadCloser.Read(p)
}

func requestBodyTimeout(next http.Handler, idle time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			controller := http.NewResponseController(w)
			// Also bound net/http's automatic draining of rejected request bodies.
			_ = controller.SetReadDeadline(time.Now().Add(idle))
			r.Body = &idleRequestBody{ReadCloser: r.Body, controller: controller, idle: idle}
			// net/http resets this before the next request. Clearing it here could
			// make post-handler draining of an incomplete body block indefinitely.
		}
		next.ServeHTTP(w, r)
	})
}
