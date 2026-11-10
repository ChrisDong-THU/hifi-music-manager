package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	library "hifi.local/library"
)

type storageInfo struct {
	Ready    bool   `json:"ready"`
	VolumeID string `json:"volume_id,omitempty"`
	Root     string `json:"root,omitempty"`
	Message  string `json:"message,omitempty"`
}

type diskController struct {
	project            string
	security           *app
	site               http.Handler
	mu                 sync.RWMutex
	reconcileMu        sync.Mutex
	active             *app
	handler            http.Handler
	cancel             context.CancelFunc
	volumeID, targetID string
	status             string
	wake               chan struct{}
	list               func(context.Context) ([]library.Drive, error)
	mounted            func(library.Drive) bool
	selectionEpoch     uint64
	selecting          bool
	diskJobMu          sync.Mutex
	diskJobs           []task
	diskTarget         string
	workers            sync.WaitGroup
	ctx                context.Context
	stopping           bool
}

type diskSelectionInput struct {
	Device     string `json:"device"`
	Root       string `json:"root"`
	MountID    string `json:"mount_id"`
	Initialize bool   `json:"initialize"`
	Confirmed  bool   `json:"confirmed"`
}

func (c *diskController) diskTasks() []task {
	c.diskJobMu.Lock()
	defer c.diskJobMu.Unlock()
	return append([]task{}, c.diskJobs...)
}

// Selection belongs to the controller, so its progress survives replacing the
// per-disk app and remains available even when there is no open database.
func (c *diskController) finishDiskTask(volume string, err error) {
	c.diskJobMu.Lock()
	defer c.diskJobMu.Unlock()
	if len(c.diskJobs) == 0 || c.diskTarget != volume {
		return
	}
	j := &c.diskJobs[len(c.diskJobs)-1]
	if j.State != "running" {
		return
	}
	j.State, j.Message = "done", "完成设置数据盘"
	if err != nil {
		j.State, j.Message = "failed", err.Error()
	}
	c.diskTarget = ""
}

func (c *diskController) listDisks(w http.ResponseWriter, r *http.Request) {
	drives, err := c.list(r.Context())
	if err != nil {
		problem(w, 503, err)
		return
	}
	pref, err := library.ReadPreference(c.project)
	if err != nil {
		problem(w, 500, err)
		return
	}
	entries := make([]library.DiskInfo, 0, len(drives))
	for _, d := range drives {
		entries = append(entries, library.InspectDisk(d))
	}
	c.mu.RLock()
	active := c.volumeID
	c.mu.RUnlock()
	respond(w, map[string]any{"enabled": true, "disks": entries, "selected_id": pref.VolumeID, "active_id": active})
}

func (c *diskController) selectDisk(w http.ResponseWriter, r *http.Request) {
	var in diskSelectionInput
	if !decode(w, r, &in) {
		return
	}
	if in.Initialize && !in.Confirmed {
		problem(w, 409, errors.New("请确认初始化数据目录"))
		return
	}
	c.diskJobMu.Lock()
	if c.stopping || (c.ctx != nil && c.ctx.Err() != nil) {
		c.diskJobMu.Unlock()
		problem(w, 503, errors.New("管理服务正在停止，请稍后重试"))
		return
	}
	if len(c.diskJobs) > 0 && c.diskJobs[len(c.diskJobs)-1].State == "running" {
		c.diskJobMu.Unlock()
		problem(w, 409, errors.New("正在设置数据盘，请等待完成"))
		return
	}
	id := uid()
	if len(c.diskJobs) >= 32 {
		c.diskJobs = c.diskJobs[len(c.diskJobs)-31:]
	}
	c.diskJobs = append(c.diskJobs, task{ID: id, Kind: "disks", State: "running", Message: "正在设置数据盘"})
	c.workers.Add(1)
	c.diskJobMu.Unlock()
	ctx := c.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer c.workers.Done()
		volume, err := c.applyDiskSelection(ctx, in)
		c.diskJobMu.Lock()
		c.diskTarget = volume
		c.diskJobMu.Unlock()
		if err != nil {
			c.finishDiskTask(volume, err)
		}
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}()
	jobResponse(w, id, nil)
}

func (c *diskController) applyDiskSelection(ctx context.Context, in diskSelectionInput) (string, error) {
	drives, err := c.list(ctx)
	if err != nil {
		return "", err
	}
	var selected *library.Drive
	for _, drive := range drives {
		if drive.Device == in.Device && drive.Root == in.Root && drive.MountID == in.MountID {
			d := drive
			selected = &d
			break
		}
	}
	if selected == nil || !selected.Writable || !c.mounted(*selected) {
		return "", errors.New("数据盘已离线、重新挂载或只读，请重新搜索")
	}
	entry := library.InspectDisk(*selected)
	if entry.Issue != "" {
		return "", errors.New(entry.Issue)
	}
	if !entry.Initialized && (!in.Initialize || !in.Confirmed) {
		return "", errors.New("请确认初始化数据目录")
	}
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		return "", err
	}
	if c.active != nil && entry.VolumeID != c.volumeID {
		if c.active.mutations.Load() != 0 {
			c.mu.Unlock()
			return "", errors.New("请等待上传或当前文件请求完成后再切换")
		}
		for _, job := range c.active.taskSnapshot() {
			if job.Kind != "disks" && fileJobActive(&job) {
				c.mu.Unlock()
				return "", errors.New("请等待当前文件任务完成后再切换")
			}
		}
	}
	// Block new file mutations and reconciliation, but do not hold the HTTP/UI
	// lock while the OS performs USB I/O or durable directory writes.
	c.selecting = true
	c.selectionEpoch++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.selecting = false
		c.mu.Unlock()
	}()
	id := entry.VolumeID
	if !entry.Initialized {
		id, err = library.InitializeDisk(*selected, c.mounted)
		if err != nil {
			return "", err
		}
	}
	if !c.mounted(*selected) {
		return "", errors.New("数据盘挂载状态已改变")
	}
	if err := library.WritePreference(c.project, id); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.targetID = id
	c.selectionEpoch++
	if id != c.volumeID {
		c.status = "正在切换数据盘"
	}
	c.mu.Unlock()
	return id, nil
}

func (c *diskController) offlineState(w http.ResponseWriter, r *http.Request, status string) {
	mode := "local"
	if c.security.lan.IsValid() {
		mode = "lan"
	}
	http.SetCookie(w, &http.Cookie{Name: c.security.cookieName, Value: c.security.csrf, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", Secure: r.TLS != nil})
	w.Header().Set("Cache-Control", "no-store")
	respond(w, serviceState{CSRF: c.security.csrf, Mode: mode, Operations: []Operation{}, Jobs: c.diskTasks(), Storage: &storageInfo{Message: status}})
}

func (c *diskController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/system/disks" {
		switch r.Method {
		case "GET":
			c.listDisks(w, r)
		case "POST":
			c.selectDisk(w, r)
		default:
			w.WriteHeader(405)
		}
		return
	}
	dav := isDAVPath(r.URL.Path)
	if !dav && !strings.HasPrefix(r.URL.Path, "/api/") {
		c.site.ServeHTTP(w, r)
		return
	}
	c.mu.RLock()
	mutation := !dav && r.Method != "GET" && r.Method != "HEAD"
	if c.selecting && mutation {
		c.mu.RUnlock()
		problem(w, 503, errors.New("正在设置数据盘，请等待完成"))
		return
	}
	ready := c.active != nil && (c.targetID == "" || c.targetID == c.volumeID) && (!dav || !c.selecting)
	a, handler, status := c.active, c.handler, c.status
	if ready {
		ready = a.beginRequest(mutation)
	}
	c.mu.RUnlock()
	if !ready {
		if r.Method == "GET" && r.URL.Path == "/api/state" {
			c.offlineState(w, r, status)
			return
		}
		problem(w, 503, errors.New("数据盘尚未就绪，请在主页设置中选择数据盘"))
		return
	}
	if dav {
		handler = http.HandlerFunc(a.serveDAV)
	}
	a.serveRequest(w, r, handler, mutation)
}

func (c *diskController) detachLocked() *app {
	old := c.active
	if c.active != nil {
		c.cancel()
		c.active = nil
		c.handler = nil
		c.volumeID = ""
	}
	return old
}

func (c *diskController) detach() {
	c.mu.Lock()
	old := c.detachLocked()
	c.mu.Unlock()
	if old != nil {
		old.close()
	}
}

func (c *diskController) reconcile(ctx context.Context) {
	c.reconcileMu.Lock()
	defer c.reconcileMu.Unlock()
	c.mu.RLock()
	epoch := c.selectionEpoch
	selecting := c.selecting
	c.mu.RUnlock()
	if selecting {
		return
	}
	selection, err := c.currentSelection(ctx)
	var volume, home string
	var guard func() error
	if err == nil {
		volume, home, guard, err = storageForSelection(selection)
	}
	pref, prefErr := library.ReadPreference(c.project)
	if prefErr != nil {
		err = prefErr
	}
	// USB enumeration may wait on the OS. Keep the offline UI independent of it.
	c.mu.Lock()
	if c.selectionEpoch != epoch || c.selecting {
		c.mu.Unlock()
		return // A newer selection superseded this discovery snapshot.
	}
	if err == nil && pref.VolumeID != "" && selection.id != pref.VolumeID {
		err = errors.New("等待指定数据盘就绪")
	}
	if err != nil {
		old := c.detachLocked()
		c.status = "请选择数据盘"
		if pref.VolumeID != "" {
			c.status = "等待指定数据盘就绪"
		}
		if pref.VolumeID != "" {
			c.finishDiskTask(pref.VolumeID, errors.New(c.status))
		}
		c.mu.Unlock()
		if old != nil {
			old.close()
		}
		return
	}
	if c.active != nil && c.volumeID == selection.id && c.active.home == home && c.active.storageMountID == selection.drive.MountID {
		c.finishDiskTask(selection.id, nil)
		c.mu.Unlock()
		return
	}
	old := c.detachLocked()
	c.status = "正在打开数据盘"
	c.mu.Unlock()
	if old != nil {
		old.close()
	}
	fail := func(message string, err error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.selectionEpoch == epoch {
			c.status = message
			c.finishDiskTask(selection.id, err)
		}
	}
	if err := prepareVolumeDirectories(volume, library.MusicPath(volume), home, guard); err != nil {
		fail("数据盘目录暂不可用", err)
		return
	}
	a, err := newAppStorage(home, library.MusicPath(volume), home, guard)
	if err != nil {
		message := "无法打开数据盘：" + err.Error()
		fail(message, errors.New(message))
		return
	}
	c.mu.Lock()
	if c.selectionEpoch != epoch || c.selecting || ctx.Err() != nil {
		c.mu.Unlock()
		a.close()
		return
	}
	a.csrf, a.cookieName, a.lan = c.security.csrf, c.security.cookieName, c.security.lan
	a.storage = &storageInfo{Ready: true, VolumeID: selection.id, Root: library.DataPath(volume)}
	a.storageMountID = selection.drive.MountID
	a.externalTasks = c.diskTasks
	appCtx, cancel := context.WithCancel(ctx)
	a.start(appCtx)
	c.active, c.handler, c.cancel, c.volumeID = a, a.routes(c.site), cancel, selection.id
	c.targetID = pref.VolumeID
	c.status = ""
	c.finishDiskTask(selection.id, nil)
	c.mu.Unlock()
}

func (c *diskController) currentSelection(ctx context.Context) (librarySelection, error) {
	pref, err := library.ReadPreference(c.project)
	if err != nil {
		return librarySelection{}, err
	}
	drives, err := c.list(ctx)
	if err != nil {
		return librarySelection{}, err
	}
	for _, d := range drives {
		if !d.Writable || !c.mounted(d) {
			continue
		}
		info := library.InspectDisk(d)
		if !info.Initialized || (pref.VolumeID != "" && info.VolumeID != pref.VolumeID) {
			continue
		}
		return librarySelection{drive: d, id: info.VolumeID}, nil
	}
	return librarySelection{}, errors.New("等待指定数据盘就绪")
}

func runDiskManager(ctx context.Context, project string, lan netip.Prefix, endpoints []managerEndpoint) error {
	auth, err := openManagerAuth(project)
	if err != nil {
		return err
	}
	defer auth.db.Close()
	dav, err := loadMusicDAV(project)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(project))
	security := &app{csrf: uid(), cookieName: "hifi_session_" + hex.EncodeToString(key[:6]), lan: lan}
	c := &diskController{project: project, security: security, site: staticSite(), status: "请选择数据盘", wake: make(chan struct{}, 1), list: library.ListDrives, mounted: library.Mounted}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.ctx = ctx
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			c.reconcile(ctx)
			select {
			case <-ctx.Done():
				c.detach()
				return
			case <-ticker.C:
			case <-c.wake:
			}
		}
	}()
	defer func() {
		cancel()
		c.diskJobMu.Lock()
		c.stopping = true
		c.diskJobMu.Unlock()
		c.workers.Wait()
		<-done
	}()
	return serveManager(ctx, endpoints, security, c, auth, c.health, dav)
}
