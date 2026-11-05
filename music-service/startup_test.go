package main

import (
	"context"
	"flag"
	"io/fs"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hifi.local/music-service/browsebridge"
)

func TestBindFailureDoesNotStartDiskSupervision(t *testing.T) {
	if home := os.Getenv("HIFI_MUSIC_BIND_HELPER"); home != "" {
		flag.CommandLine = flag.NewFlagSet("music", flag.ContinueOnError)
		os.Args = []string{"music", "-home", home}
		if err := run(); err == nil {
			t.Fatal("expected bind failure")
		}
		if _, err := os.Stat(filepath.Join(home, "logs")); !os.IsNotExist(err) {
			t.Fatal("failed startup touched the player supervision state")
		}
		return
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "music-service"), 0700); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	config := `{"browse_listen":"10.254.254.254:` + port + `","lan_cidr":"10.0.0.0/8","browse_udn":"uuid:12345678-1234-1234-1234-123456789012","backend_description":"http://127.0.0.1:9791/device.xml"}`
	if err := os.WriteFile(filepath.Join(home, "music-service/config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBindFailureDoesNotStartDiskSupervision$")
	child.Env = append(os.Environ(), "HIFI_MUSIC_BIND_HELPER="+home)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("startup regression: %v %s", err, output)
	}
}

func TestConfigurationRejectsTyposTrailingContentAndOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, data := range []string{`{"lan_cird":"10.0.0.0/8"}`, `{} {}`, strings.Repeat(" ", 64<<10) + `{}`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readServiceConfig(path); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestStandaloneMusicBinaryCarriesItsDependencyNotices(t *testing.T) {
	b, err := browsebridge.New(browsebridge.Config{Listen: "192.168.1.5:9792", BackendDescription: "http://127.0.0.1:9791/device.xml", UDN: "uuid:12345678-1234-1234-1234-123456789012", Notices: notices})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(notices, "licenses")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"/THIRD_PARTY_NOTICES.txt"}
	for _, entry := range entries {
		paths = append(paths, "/licenses/"+entry.Name())
	}
	for _, path := range paths {
		r := httptest.NewRequest("GET", "http://192.168.1.5:9792"+path, nil)
		r.RemoteAddr = "192.168.1.3:1234"
		w := httptest.NewRecorder()
		b.Handler().ServeHTTP(w, r)
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatal("missing embedded notice", path, w.Code)
		}
	}
}
