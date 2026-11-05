package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	library "hifi.local/library"
)

func TestSlowDiskDiscoveryDoesNotBlockOfflineState(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := &diskController{project: t.TempDir(), security: &app{csrf: "token", cookieName: "session"}, standalone: true,
		list:    func(context.Context) ([]library.Drive, error) { close(entered); <-release; return nil, nil },
		mounted: func(library.Drive) bool { return true }}
	go func() { c.reconcile(context.Background()); close(finished) }()
	<-entered
	responded := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		c.ServeHTTP(w, httptest.NewRequest("GET", "/api/state", nil))
		responded <- w.Code
	}()
	select {
	case code := <-responded:
		close(release)
		<-finished
		if code != http.StatusOK {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		close(release)
		<-finished
		<-responded
		t.Fatal("USB discovery blocked the offline UI")
	}
}

func TestDiskSettingsWorkWithoutAnIndexAndRequireInitializationConfirmation(t *testing.T) {
	mount, project := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(mount)
	if err != nil {
		t.Fatal(err)
	}
	id, err := library.DirectoryMountID(root)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	drive := library.Drive{Root: mount, Device: "/dev/test", MountID: id, Writable: true, Name: "USB"}
	keep := filepath.Join(mount, "personal.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	gate := &app{csrf: "token", cookieName: "session"}
	c := &diskController{project: project, security: gate, site: http.NotFoundHandler(), wake: make(chan struct{}, 1), list: func(context.Context) ([]library.Drive, error) { return []library.Drive{drive}, nil }, mounted: func(d library.Drive) bool { return d.Root == mount && d.MountID == id }}
	request := func(method, path string, body any, authorized bool) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, bytes.NewReader(raw))
		r.RemoteAddr = "127.0.0.1:1234"
		if authorized {
			r.AddCookie(&http.Cookie{Name: "session", Value: "token"})
			r.Header.Set("X-CSRF-Token", "token")
		}
		w := httptest.NewRecorder()
		gate.protect(c).ServeHTTP(w, r)
		return w
	}
	if w := request("GET", "/api/state", nil, false); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"ready":false`)) {
		t.Fatal("settings bootstrap needs an index", w.Body.String())
	}
	w := request("GET", "/api/system/disks", nil, false)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"initialized":false`)) {
		t.Fatal(w.Body.String())
	}
	body := map[string]any{"device": drive.Device, "root": drive.Root, "mount_id": id, "initialize": true}
	if w := request("POST", "/api/system/disks", body, false); w.Code != 403 {
		t.Fatal("selection bypassed CSRF")
	}
	if w := request("POST", "/api/system/disks", body, true); w.Code != 409 {
		t.Fatal("initialized without confirmation")
	}
	if _, err := os.Stat(library.DataPath(mount)); !os.IsNotExist(err) {
		t.Fatal("confirmation rejection wrote data")
	}
	body["confirmed"] = true
	body["mount_id"] = "replaced"
	if w := request("POST", "/api/system/disks", body, true); w.Code != http.StatusAccepted {
		t.Fatal(w.Body.String())
	}
	c.workers.Wait()
	if jobs := c.diskTasks(); len(jobs) != 1 || jobs[0].State != "failed" {
		t.Fatal("accepted stale mount", jobs)
	}
	body["mount_id"] = id
	if w := request("POST", "/api/system/disks", body, true); w.Code != http.StatusAccepted {
		t.Fatal(w.Body.String())
	}
	c.workers.Wait()
	info := library.InspectDisk(drive)
	if !info.Initialized {
		t.Fatal("layout was not initialized", info.Issue)
	}
	pref, err := library.ReadPreference(project)
	if err != nil || pref.VolumeID != info.VolumeID {
		t.Fatal("selection was not persisted", err)
	}
	if raw, _ := os.ReadFile(keep); string(raw) != "keep" {
		t.Fatal("modified unrelated disk data")
	}
	if _, err := os.Stat(filepath.Join(mount, "music")); !os.IsNotExist(err) {
		t.Fatal("created data outside the project root")
	}
}

func TestDiskInitializationOutlivesRequestAndDoesNotBlockProgress(t *testing.T) {
	mount := t.TempDir()
	root, err := os.OpenRoot(mount)
	if err != nil {
		t.Fatal(err)
	}
	id, err := library.DirectoryMountID(root)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	drive := library.Drive{Device: "test", Root: mount, MountID: id, Writable: true}
	entered, release := make(chan struct{}), make(chan struct{})
	calls := 0
	c := &diskController{project: t.TempDir(), security: &app{csrf: "token", cookieName: "session"},
		list: func(context.Context) ([]library.Drive, error) { return []library.Drive{drive}, nil },
		mounted: func(library.Drive) bool {
			calls++
			if calls == 2 { // The first live check inside InitializeDisk.
				close(entered)
				<-release
			}
			return true
		}}
	defer c.workers.Wait()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	raw, _ := json.Marshal(diskSelectionInput{Device: drive.Device, Root: mount, MountID: id, Initialize: true, Confirmed: true})
	w := httptest.NewRecorder()
	c.ServeHTTP(w, httptest.NewRequest("POST", "/api/system/disks", bytes.NewReader(raw)).WithContext(ctx))
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Body.String())
	}
	cancel() // Closing the submitting browser must not cancel an accepted task.
	<-entered
	responded := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		state := httptest.NewRecorder()
		c.ServeHTTP(state, httptest.NewRequest("GET", "/api/state", nil))
		responded <- state
	}()
	select {
	case state := <-responded:
		if state.Code != 200 || !bytes.Contains(state.Body.Bytes(), []byte(`"state":"running"`)) {
			t.Fatal("initialization progress unavailable", state.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("USB initialization blocked the state poll")
	}
	duplicate := httptest.NewRecorder()
	c.ServeHTTP(duplicate, httptest.NewRequest("POST", "/api/system/disks", bytes.NewReader(raw)))
	if duplicate.Code != http.StatusConflict {
		t.Fatal("concurrent selection accepted", duplicate.Code)
	}
	close(release)
	c.workers.Wait()
	info := library.InspectDisk(drive)
	pref, err := library.ReadPreference(c.project)
	if !info.Initialized || err != nil || pref.VolumeID != info.VolumeID {
		t.Fatal("accepted initialization was cancelled with its HTTP request", info, err)
	}
}

func TestDiskSwitchRejectsInFlightUploadsBeforeWritingPreference(t *testing.T) {
	mount, project := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(mount)
	if err != nil {
		t.Fatal(err)
	}
	id, err := library.DirectoryMountID(root)
	root.Close()
	if err != nil {
		t.Fatal(err)
	}
	drive := library.Drive{Device: "test", Root: mount, MountID: id, Writable: true}
	if _, err := library.InitializeDisk(drive, func(library.Drive) bool { return true }); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	a.mutations.Add(1)
	c := &diskController{project: project, active: a, volumeID: "old-disk", list: func(context.Context) ([]library.Drive, error) { return []library.Drive{drive}, nil }, mounted: func(library.Drive) bool { return true }}
	if _, err := c.applyDiskSelection(context.Background(), diskSelectionInput{Device: drive.Device, Root: mount, MountID: id}); err == nil {
		t.Fatal("switched while an upload was in flight")
	}
	if _, err := os.Stat(filepath.Join(project, "config", "data-disk.json")); !os.IsNotExist(err) {
		t.Fatal("rejected switch changed the selection")
	}
}
