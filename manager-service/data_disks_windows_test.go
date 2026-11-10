//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	library "hifi.local/library"
)

func TestStandaloneDiskSwitchClosesIndexesAndKeepsSettingsAvailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	project := t.TempDir()
	var disks []library.Drive
	for _, mount := range []string{t.TempDir(), t.TempDir()} {
		root, err := os.OpenRoot(mount)
		if err != nil {
			t.Fatal(err)
		}
		id, err := library.DirectoryMountID(root)
		root.Close()
		if err != nil {
			t.Fatal(err)
		}
		d := library.Drive{Device: mount, Root: mount, MountID: id, Writable: true}
		if _, err := library.InitializeDisk(d, func(library.Drive) bool { return true }); err != nil {
			t.Fatal(err)
		}
		disks = append(disks, d)
	}
	aID, bID := library.InspectDisk(disks[0]).VolumeID, library.InspectDisk(disks[1]).VolumeID
	c := &diskController{project: project, security: &app{csrf: "test", cookieName: "test"}, site: http.NotFoundHandler(),
		list:    func(context.Context) ([]library.Drive, error) { return disks, nil },
		mounted: func(library.Drive) bool { return true }}
	defer c.detach()
	c.reconcile(ctx)
	if c.active == nil || c.volumeID != aID {
		t.Fatal("first disk did not open", c.status)
	}
	if err := c.active.put("batches", "only-a", Batch{ID: "only-a", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	// Exercise the async HTTP path, including its own running task appearing in
	// the old app's snapshot. It must not count as a conflicting file operation.
	deadline := time.Now().Add(3 * time.Second)
	for {
		busy := false
		for _, j := range c.active.taskSnapshot() {
			busy = busy || j.State == "running" || j.State == "queued"
		}
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial scan did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	before := httptest.NewRecorder()
	c.ServeHTTP(before, httptest.NewRequest("GET", "/api/state", nil))
	raw, _ := json.Marshal(diskSelectionInput{Device: disks[1].Device, Root: disks[1].Root, MountID: disks[1].MountID})
	selected := httptest.NewRecorder()
	c.ServeHTTP(selected, httptest.NewRequest("POST", "/api/system/disks", bytes.NewReader(raw)))
	if selected.Code != http.StatusAccepted {
		t.Fatal(selected.Body.String())
	}
	c.workers.Wait()
	if jobs := c.diskTasks(); len(jobs) != 1 || jobs[0].State != "running" {
		t.Fatal("task finished before opening the selected disk", jobs)
	}
	c.reconcile(ctx)
	if c.active == nil || c.volumeID != bID {
		t.Fatal("second disk did not open", c.status)
	}
	poll := httptest.NewRequest("GET", "/api/state", nil)
	poll.Header.Set("If-None-Match", before.Header().Get("ETag"))
	after := httptest.NewRecorder()
	c.ServeHTTP(after, poll)
	if after.Code != 200 || !bytes.Contains(after.Body.Bytes(), []byte(`"kind":"disks","state":"done"`)) {
		t.Fatal("completion lost when replacing the index", after.Code, after.Body.String())
	}
	if batches, err := readAll[Batch](c.active, "batches"); err != nil || len(batches) != 0 {
		t.Fatal("second disk inherited the first disk's drafts", err)
	}
	second := disks[1]
	disks = disks[:1]
	c.reconcile(ctx)
	if c.active != nil {
		t.Fatal("missing preferred disk silently fell back")
	}
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("GET", "/api/state", nil))
	if w.Code != http.StatusOK {
		t.Fatal("settings bootstrap disappeared with disk", w.Code)
	}
	disks = append(disks, second)
	if err := library.WritePreference(project, aID); err != nil {
		t.Fatal(err)
	}
	c.reconcile(ctx)
	if c.active == nil || c.volumeID != aID {
		t.Fatal("original disk did not reopen", c.status)
	}
	if batches, err := readAll[Batch](c.active, "batches"); err != nil || len(batches) != 1 || batches[0].ID != "only-a" {
		t.Fatal("original drafts were not retained", err)
	}
}
