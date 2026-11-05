package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

func libraryRescan(endpoint string) (func(context.Context) error, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("MinimServer 刷新地址必须是本机 HTTP 地址")
	}
	client := &http.Client{Timeout: 70 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader("rescan"))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "text/plain")
		resp, err := client.Do(req)
		if err != nil {
			return errors.New("播放曲库刷新失败，请检查 MinimServer 后重新扫描")
		}
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("MinimServer 刷新返回 %d，请重新扫描", resp.StatusCode)
		}
		return nil
	}, nil
}

func updatesPlaybackLibrary(kind string) bool {
	return kind == "scan" || kind == "import" || kind == "edit" || kind == "delete"
}
