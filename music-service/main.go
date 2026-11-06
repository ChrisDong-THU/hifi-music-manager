package main

// This process only maintains the mounted music library and the existing
// UPnP presentation endpoint. It has no web management API, accounts or database.
import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"hifi.local/music-service/browsebridge"
)

var errOffline = errors.New("music USB is not mounted")

//go:embed licenses/* THIRD_PARTY_NOTICES.txt
var notices embed.FS

type LibraryService struct {
	home string
}
type serviceConfig struct {
	LANCIDR            string `json:"lan_cidr"`
	Name               string `json:"name"`
	BrowseUDN          string `json:"browse_udn"`
	BrowseListen       string `json:"browse_listen"`
	BackendDescription string `json:"backend_description"`
}

func readServiceConfig(path string) (serviceConfig, error) {
	var config serviceConfig
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return config, err
	}
	if len(data) > 64<<10 {
		return config, errors.New("music service configuration exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, errors.New("music service configuration contains extra data")
	}
	return config, nil
}

func newID() string {
	data := make([]byte, 24)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data)
}
func requestRescan(ctx context.Context) error {
	const endpoint = "http://127.0.0.1:9790/"
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader("rescan"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	client := http.Client{Timeout: 65 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("MinimServer 暂不可用")
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20)); err != nil {
		return fmt.Errorf("MinimServer 刷新响应未完成: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("MinimServer 返回 %d", resp.StatusCode)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	userHome, _ := os.UserHomeDir()
	home := flag.String("home", filepath.Join(userHome, "hifi"), "music service root")
	check := flag.Bool("check", false, "validate existing configuration without starting services")
	minimRuntime := flag.Bool("minim-runtime", false, "run MinimServer using the selected disk's private index")
	bubbleDomain := flag.String("bubble-relay-domain", "", "serve BubbleUPnP through the specified public domain")
	bubbleUpstream := flag.String("bubble-relay-upstream", "", "literal private IPv4 address of this device's BubbleUPnP Server")
	flag.Parse()
	if *bubbleDomain != "" || *bubbleUpstream != "" {
		if *check || *minimRuntime {
			return errors.New("BubbleUPnP relay is a separate service mode")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return runBubbleRelay(ctx, *home, *bubbleDomain, *bubbleUpstream)
	}
	config, err := readServiceConfig(filepath.Join(*home, "music-service", "config.json"))
	if err != nil {
		return err
	}
	service := &LibraryService{home: *home}
	bridge, err := browsebridge.New(browsebridge.Config{Listen: config.BrowseListen, BackendDescription: config.BackendDescription, UDN: config.BrowseUDN, Name: config.Name, LANCIDR: config.LANCIDR, Resources: service.openArtists, ResourceKey: service.libraryKey, Notices: notices})
	if err != nil {
		return err
	}
	if *check {
		fmt.Println("Music service configuration valid; no management listener configured")
		return nil
	}
	if *minimRuntime {
		return runMinimRuntime(*home, config)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Reserve the presentation address before supervision can stop/restart the
	// vendor player. A duplicate/failed start must not disturb a running service.
	listener, err := net.Listen("tcp", config.BrowseListen)
	if err != nil {
		return err
	}
	defer listener.Close()
	var group sync.WaitGroup
	group.Add(1)
	go func() { defer group.Done(); service.watchLibrary(ctx) }()
	err = bridge.Serve(ctx, listener)
	cancel()
	group.Wait()
	return err
}
