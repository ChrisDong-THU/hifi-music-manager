package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestManagerAuthenticationLifecycle(t *testing.T) {
	directory := t.TempDir()
	auth, err := openManagerAuth(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = auth.db.Close() })
	gate := &app{cookieName: "csrf", csrf: "legacy-token"}
	handler := gate.protectManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), auth, func() managerHealth { return managerHealth{Service: "hifi-manager"} })

	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:2222"
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		// An old CSRF token must never be enough to authenticate.
		r.AddCookie(&http.Cookie{Name: "csrf", Value: "legacy-token"})
		r.Header.Set("X-CSRF-Token", "legacy-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got status %d, want %d: %s", w.Code, status, w.Body.String())
		}
	}
	for _, path := range []string{"/api/state", "//api/state", "/api/system/disks", "/api/tracks/1/audio", "/api/tracks/1/cover"} {
		expect(request(http.MethodGet, path, "", nil), http.StatusUnauthorized)
	}
	expect(request(http.MethodPost, "/api/scan", "{}", nil), http.StatusUnauthorized)
	expect(request(http.MethodGet, "/healthz", "", nil), http.StatusOK)
	expect(request(http.MethodGet, "/", "", nil), http.StatusNoContent)

	password := " 密码 Jazz! 2026 "
	code := newManagerInvite(auth.inviteKey, "owner", time.Now())
	registration := func(username, password, code string) string {
		body, _ := json.Marshal(managerCredentials{Username: username, Password: password, InviteCode: code})
		return string(body)
	}
	register := func(username, password, code string) *httptest.ResponseRecorder {
		return request(http.MethodPost, "/api/auth/register", registration(username, password, code), nil)
	}
	expect(register("owner", password, "invalid"), http.StatusUnauthorized)
	expect(register("other", password, code), http.StatusUnauthorized)
	expired := newManagerInvite(auth.inviteKey, "owner", time.Now().Add(-managerInviteTTL))
	expect(register("owner", password, expired), http.StatusUnauthorized)
	for _, invalid := range []string{"short", "        ", strings.Repeat("音", 86)} {
		expect(register("owner", invalid, code), http.StatusBadRequest)
	}

	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { results <- register("owner", password, code) }()
	}
	first, second := <-results, <-results
	if first.Code != http.StatusOK {
		first, second = second, first
	}
	expect(first, http.StatusOK)
	expect(second, http.StatusConflict)
	cookies := first.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatal("registration did not issue a protected session cookie")
	}
	cookie := cookies[0]
	expect(request(http.MethodGet, "/api/state", "", cookie), http.StatusNoContent)
	for _, removed := range []string{"/api/auth/setup", "/api/auth/logout", "/api/auth/password"} {
		expect(request(http.MethodPost, removed, "{}", cookie), http.StatusNotFound)
	}
	expect(request(http.MethodGet, "/api/state", "", cookie), http.StatusNoContent)
	other := newManagerInvite(auth.inviteKey, "other", time.Now())
	for range 7 { // Three invalid invitations were already rejected above.
		expect(register("other", "Another password", "invalid"), http.StatusUnauthorized)
	}
	expect(register("other", "Another password", "invalid"), http.StatusTooManyRequests)
	expect(register("other", "Another password", other), http.StatusOK)
	expect(request(http.MethodGet, "/api/state", "", cookie), http.StatusNoContent)

	if err := auth.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openManagerAuth(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	handler = gate.protectManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), reopened, func() managerHealth { return managerHealth{Service: "hifi-manager"} })
	expect(request(http.MethodGet, "/api/state", "", cookie), http.StatusUnauthorized)
	expect(register("owner", password, code), http.StatusConflict)
	ownerLogin := request(http.MethodPost, "/api/auth/login", registration("owner", password, ""), nil)
	expect(ownerLogin, http.StatusOK)
	expect(request(http.MethodPost, "/api/auth/login", registration("other", "Another password", ""), nil), http.StatusOK)
	ownerCookie := ownerLogin.Result().Cookies()[0]
	reopened.sessions[sha256.Sum256([]byte(ownerCookie.Value))] = time.Now().Add(-time.Second)
	expect(request(http.MethodGet, "/api/state", "", ownerCookie), http.StatusUnauthorized)
}

func TestManagerAuthenticationTransportAndRateLimit(t *testing.T) {
	directory := t.TempDir()
	auth, err := openManagerAuth(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = auth.db.Close() })
	if err := auth.registerAccount(managerCredentials{Username: "owner", Password: "Test #042681", InviteCode: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := auth.registerAccount(managerCredentials{Username: "other", Password: "Another password", InviteCode: "other-fixture"}); err != nil {
		t.Fatal(err)
	}
	auth.loginLimits["owner"] = managerLoginLimit{Failures: loginMaxFailures - 1, WindowUntil: time.Now().Add(loginCooldown)}
	gate := &app{lan: netip.MustParsePrefix("192.168.10.0/24")}
	handler := gate.protectManager(http.NotFoundHandler(), auth, func() managerHealth { return managerHealth{} })
	request := func(scheme, origin, contentType, username, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(managerCredentials{Username: username, Password: password})
		r := httptest.NewRequest(http.MethodPost, scheme+"://192.168.10.4:8787/api/auth/login", strings.NewReader(string(body)))
		r.RemoteAddr = "192.168.10.5:2222"
		if scheme == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Set("Origin", origin)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, test := range []struct {
		scheme, origin, media string
		want                  int
	}{
		{"http", "", "application/json", http.StatusForbidden},
		{"https", "https://other.example", "application/json", http.StatusForbidden},
		{"https", "", "text/plain", http.StatusUnsupportedMediaType},
		{"https", "https://192.168.10.4:8787", "application/json", http.StatusUnauthorized},
		{"https", "https://192.168.10.4:8787", "application/json", http.StatusTooManyRequests},
	} {
		w := request(test.scheme, test.origin, test.media, "owner", "829413")
		if w.Code != test.want {
			t.Errorf("got %d, want %d for %+v", w.Code, test.want, test)
		}
		if test.want == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Error("rate limit response must include Retry-After")
		}
	}
	for range loginMaxFailures {
		if w := request("https", "", "application/json", "unknown", "invalid"); w.Code != http.StatusUnauthorized {
			t.Fatalf("unknown username: got %d", w.Code)
		}
	}
	if w := request("https", "", "application/json", "unknown", "invalid"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("unknown username: got %d, want 429", w.Code)
	}
	if w := request("https", "", "application/json", "other", "Another password"); w.Code != http.StatusOK {
		t.Fatalf("other account must still be able to sign in: got %d", w.Code)
	} else if cookies := w.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatal("HTTPS login must issue a Secure session cookie")
	}
	if err := auth.db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openManagerAuth(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if limit := reopened.loginLimits["owner"]; limit.Failures != loginMaxFailures || !time.Now().Before(limit.BlockedUntil) {
		t.Fatal("the failed-login cooldown must survive a restart")
	}
}

func TestStoredManagerPasswordsStillWork(t *testing.T) {
	for _, old := range []struct {
		password string
		pin      bool
	}{
		{"correct horse battery staple", false},
		{"042681", true},
	} {
		directory := t.TempDir()
		auth, err := openManagerAuth(directory)
		if err != nil {
			t.Fatal(err)
		}
		salt := make([]byte, 16)
		record, _ := json.Marshal(struct {
			managerAccount
			NumericPIN bool `json:"numeric_pin,omitempty"`
		}{managerAccount{Username: "owner", Salt: salt, Digest: managerPasswordDigest(old.password, salt)}, old.pin})
		if err := auth.db.Update(func(tx *bolt.Tx) error {
			bucket, err := tx.CreateBucket(authBucket)
			if err != nil {
				return err
			}
			return bucket.Put(accountKey, record)
		}); err != nil {
			t.Fatal(err)
		}
		if err := auth.db.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := openManagerAuth(directory)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(managerCredentials{Username: "owner", Password: old.password})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		reopened.credentials(w, r, "auth")
		_ = reopened.db.Close()
		if w.Code != http.StatusOK {
			t.Fatalf("stored password login failed: %d %s", w.Code, w.Body.String())
		}
	}
}
