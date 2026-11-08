package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const managerTunnelListen = "127.0.0.1:18787"

var managerDomainPattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

type managerEndpoint struct {
	listen       string
	cert, key    string
	publicDomain string
	lanHostname  string
}

func managerEndpoints(local managerEndpoint, domain, cert, key string) ([]managerEndpoint, error) {
	if (local.cert == "") != (local.key == "") {
		return nil, errors.New("tls-cert 和 tls-key 必须一起指定")
	}
	if local.lanHostname != "" {
		local.lanHostname = strings.ToLower(local.lanHostname)
		if len(local.lanHostname) > 253 || !managerDomainPattern.MatchString(local.lanHostname) ||
			!strings.HasSuffix(local.lanHostname, ".local") || local.cert == "" {
			return nil, errors.New("lan-hostname 需要 .local 主机名和局域网 HTTPS 证书")
		}
	}
	if domain == "" && cert == "" && key == "" {
		return []managerEndpoint{local}, nil
	}
	if len(domain) > 253 || !managerDomainPattern.MatchString(domain) || net.ParseIP(domain) != nil || cert == "" || key == "" {
		return nil, errors.New("公网入口需要完整域名、public-tls-cert 和 public-tls-key")
	}
	return []managerEndpoint{local, {listen: managerTunnelListen, cert: cert, key: key, publicDomain: strings.ToLower(domain)}}, nil
}

func serveManager(ctx context.Context, endpoints []managerEndpoint, gate *app, next http.Handler, auth *managerAuth, health func() managerHealth) error {
	type boundEndpoint struct {
		server   *http.Server
		listener net.Listener
	}
	var bound []boundEndpoint
	for _, endpoint := range endpoints {
		handler := gate.protectManager(next, auth, health, endpoint.publicDomain, endpoint.lanHostname)
		server := &http.Server{Addr: endpoint.listen, Handler: requestBodyTimeout(handler, 2*time.Minute),
			ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
		if endpoint.cert != "" {
			pair, err := tls.LoadX509KeyPair(endpoint.cert, endpoint.key)
			if err != nil {
				return fmt.Errorf("加载 %s 的管理 HTTPS 证书：%w", endpoint.listen, err)
			}
			name := endpoint.publicDomain
			if name == "" {
				name = endpoint.lanHostname
			}
			if name != "" {
				leaf, err := x509.ParseCertificate(pair.Certificate[0])
				if err != nil {
					return err
				}
				if err := leaf.VerifyHostname(name); err != nil {
					return fmt.Errorf("管理 HTTPS 证书不覆盖 %s：%w", name, err)
				}
			}
			server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}}
		}
		listener, err := net.Listen("tcp", endpoint.listen)
		if err != nil {
			return err
		}
		defer listener.Close()
		bound = append(bound, boundEndpoint{server, listener})
	}
	served := make(chan error, len(bound))
	for _, entry := range bound {
		go func() {
			if entry.server.TLSConfig != nil {
				log.Printf("hifi-music-nano · https://%s", entry.listener.Addr())
				served <- entry.server.ServeTLS(entry.listener, "", "")
				return
			}
			log.Printf("hifi-music-nano · http://%s", entry.listener.Addr())
			served <- entry.server.Serve(entry.listener)
		}()
	}
	var err error
	select {
	case err = <-served:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var closing sync.WaitGroup
	for _, entry := range bound {
		closing.Go(func() {
			if entry.server.Shutdown(shutdown) != nil {
				_ = entry.server.Close()
			}
		})
	}
	closing.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
