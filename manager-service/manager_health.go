package main

import "net/http"

type managerHealth struct {
	Service      string `json:"service"`
	Busy         bool   `json:"busy"`
	StorageReady bool   `json:"storage_ready"`
}

func fileJobActive(job *task) bool {
	return job.Kind != "sync" && (job.State == "queued" || job.State == "running")
}

func (a *app) fileJobsActive() bool {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	for _, job := range a.jobs {
		if fileJobActive(job) {
			return true
		}
	}
	return false
}

func (a *app) health() managerHealth {
	return managerHealth{
		Service:      "hifi-manager",
		Busy:         a.mutations.Load() > 0 || a.fileJobsActive(),
		StorageReady: a.checkStorage() == nil,
	}
}

func (c *diskController) diskJobsActive() bool {
	c.diskJobMu.Lock()
	defer c.diskJobMu.Unlock()
	for i := range c.diskJobs {
		if fileJobActive(&c.diskJobs[i]) {
			return true
		}
	}
	return false
}

func (c *diskController) health() managerHealth {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state := managerHealth{Service: "hifi-manager", Busy: c.selecting || c.diskJobsActive()}
	if c.active != nil {
		health := c.active.health()
		state.Busy = state.Busy || health.Busy
		state.StorageReady = health.StorageReady && (c.targetID == "" || c.targetID == c.volumeID)
	}
	return state
}

func serveManagerHealth(w http.ResponseWriter, r *http.Request, health func() managerHealth) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	respond(w, health())
}
