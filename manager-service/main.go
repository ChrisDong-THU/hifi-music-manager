package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	library "hifi.local/library"
)

//go:embed web/* licenses/* THIRD_PARTY_NOTICES.txt
var assets embed.FS

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

type managerTLS struct{ cert, key string }

func (t managerTLS) scheme() string {
	if t.cert != "" {
		return "https"
	}
	return "http"
}

func (t managerTLS) serve(server *http.Server, listener net.Listener) error {
	if t.cert != "" {
		return server.ServeTLS(listener, t.cert, t.key)
	}
	return server.Serve(listener)
}

func run() error {
	home := flag.String("home", "../downloads/manager-local", "database and service directory")
	libraryHome := flag.String("library-home", "", "project root: select all storage from the current USB library")
	dataDisks := flag.Bool("data-disks", false, "manage mounted USB data disks without a playback supervisor")
	listen := flag.String("listen", "127.0.0.1:8787", "explicit listening IP and port")
	lan := flag.String("allow-lan", "", "optional private LAN CIDR")
	cert := flag.String("tls-cert", "", "PEM certificate for HTTPS management")
	key := flag.String("tls-key", "", "PEM private key for HTTPS management")
	music := flag.String("music", "", "optional external music directory")
	data := flag.String("data", "", "optional upload directory")
	volume := flag.String("volume", "", "Linux mount point required for external USB storage")
	rescanURL := flag.String("rescan-url", "", "optional loopback MinimServer rescan URL")
	invite := flag.String("invite", "", "issue a one-time registration invitation for this username")
	flag.Parse()
	if *invite != "" {
		directory := *home
		if *libraryHome != "" {
			directory = *libraryHome
		}
		return printManagerInvite(directory, *invite)
	}
	if (*cert == "") != (*key == "") {
		return fmt.Errorf("tls-cert 和 tls-key 必须一起指定")
	}
	transport := managerTLS{cert: *cert, key: *key}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	allowed, err := accessNetwork(*listen, *lan)
	if err != nil {
		return err
	}
	if *libraryHome != "" {
		project, err := filepath.Abs(*libraryHome)
		if err != nil {
			return err
		}
		if *dataDisks {
			return fmt.Errorf("library-home 和 data-disks 不能同时使用")
		}
		return runDiskManager(ctx, project, *listen, allowed, *rescanURL, false, transport)
	}
	abs, err := filepath.Abs(*home)
	if err != nil {
		return err
	}
	if *dataDisks {
		if *music != "" || *data != "" || *volume != "" {
			return fmt.Errorf("数据盘管理模式不能指定独立的 music、data 或 volume")
		}
		return runDiskManager(ctx, abs, *listen, allowed, *rescanURL, true, transport)
	}
	musicPath, dataPath := filepath.Join(abs, library.MusicDirectory), filepath.Join(abs, library.ManagerDirectory)
	if *music != "" {
		musicPath, err = filepath.Abs(*music)
		if err != nil {
			return err
		}
	}
	if *data != "" {
		dataPath, err = filepath.Abs(*data)
		if err != nil {
			return err
		}
	}
	var guard func() error
	if *volume != "" {
		volumePath, e := filepath.Abs(*volume)
		if e != nil {
			return e
		}
		for _, p := range []string{musicPath, dataPath} {
			rel, e := filepath.Rel(volumePath, p)
			if e != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("音乐和上传目录必须位于指定音乐卷内")
			}
		}
		guard, err = volumeGuard(volumePath)
		if err != nil {
			return err
		}
		if err = prepareVolumeDirectories(volumePath, musicPath, dataPath, guard); err != nil {
			return err
		}
	}
	var rescan func(context.Context) error
	if *rescanURL != "" {
		rescan, err = libraryRescan(*rescanURL)
		if err != nil {
			return err
		}
	}
	auth, err := openManagerAuth(abs)
	if err != nil {
		return err
	}
	defer auth.db.Close()
	a, err := newAppStorage(filepath.Join(abs, library.ManagerDirectory), musicPath, dataPath, guard)
	if err != nil {
		return err
	}
	defer func() { stop(); a.close() }()
	a.lan, a.rescan = allowed, rescan
	storageLost := make(chan error, 1)
	if guard != nil {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := guard(); err != nil {
						storageLost <- err
						stop()
						return
					}
				}
			}
		}()
	}
	a.start(ctx)
	mux := a.routes(staticSite())
	handler := a.protectManager(a.trackRequests(mux), auth, a.health)
	server := &http.Server{Addr: *listen, Handler: requestBodyTimeout(handler, 2*time.Minute), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
	}()
	log.Printf("hifi-music-nano · %s://%s · 数据 %s · 曲库 %s", transport.scheme(), l.Addr(), abs, musicPath)
	err = transport.serve(server, l)
	if err == http.ErrServerClosed {
		select {
		case err := <-storageLost:
			return err
		default:
		}
		return nil
	}
	return err
}
