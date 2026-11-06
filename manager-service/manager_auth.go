package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"hash/maphash"
	"log"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
)

const (
	passwordRounds   = 600_000
	loginCooldown    = 5 * time.Minute
	loginMaxFailures = 5
	sessionLifetime  = 12 * time.Hour
	maxPasswordBytes = 256
)

var (
	authBucket        = []byte("admin")
	accountKey        = []byte("account") // Original single-account record.
	accountsBucket    = []byte("accounts")
	loginLimitKey     = []byte("login-limit")
	usedInvitesBucket = []byte("used-invites")
	errInviteInvalid  = errors.New("邀请码无效、已使用或已过期")
)

type managerAccount struct {
	Username string `json:"username"`
	Salt     []byte `json:"salt"`
	Digest   []byte `json:"digest"`
}

type managerLoginLimit struct {
	Failures     int       `json:"failures"`
	WindowUntil  time.Time `json:"window_until"`
	BlockedUntil time.Time `json:"blocked_until"`
}

func (l *managerLoginLimit) recordFailure(now time.Time, window time.Duration, maxFailures int) {
	if !now.Before(l.WindowUntil) || !l.BlockedUntil.IsZero() {
		*l = managerLoginLimit{WindowUntil: now.Add(window)}
	}
	l.Failures++
	if l.Failures >= maxFailures {
		l.BlockedUntil = now.Add(window)
	}
}

type managerAuth struct {
	db        *bolt.DB
	inviteKey []byte

	// Credential operations serialize password hashing and account writes without
	// blocking requests that already have a session.
	credentialSlot    chan struct{}
	accounts          map[string]managerAccount
	loginLimits       map[string]managerLoginLimit
	anonymousSeed     maphash.Seed
	anonymousLimits   [64]managerLoginLimit
	registrationLimit managerLoginLimit

	sessionMu sync.Mutex
	sessions  map[[sha256.Size]byte]time.Time
}

func openManagerAuth(directory string) (*managerAuth, error) {
	config := filepath.Join(directory, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(config, "manager-auth.db"), 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	auth := &managerAuth{
		db: db, credentialSlot: make(chan struct{}, 1),
		accounts: make(map[string]managerAccount), loginLimits: make(map[string]managerLoginLimit),
		anonymousSeed: maphash.MakeSeed(), sessions: make(map[[sha256.Size]byte]time.Time),
	}
	if err := restrictAuthFile(db.Path()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := auth.load(); err != nil {
		_ = db.Close()
		return nil, err
	}
	auth.inviteKey, err = loadManagerInviteKey(config)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return auth, nil
}

func (a *managerAuth) load() error {
	loadAccount := func(record []byte) error {
		var account managerAccount
		if err := json.Unmarshal(record, &account); err != nil {
			return err
		}
		if !validManagerUsername(account.Username) || len(account.Salt) != 16 || len(account.Digest) != sha256.Size {
			return errors.New("管理账号配置无效")
		}
		if _, exists := a.accounts[account.Username]; exists {
			return errors.New("管理账号重复")
		}
		a.accounts[account.Username] = account
		return nil
	}
	return a.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(authBucket)
		if bucket == nil {
			return nil
		}
		if record := bucket.Get(accountKey); record != nil {
			if err := loadAccount(record); err != nil {
				return err
			}
		}
		if accounts := bucket.Bucket(accountsBucket); accounts != nil {
			if err := accounts.ForEach(func(_, record []byte) error { return loadAccount(record) }); err != nil {
				return err
			}
		}
		if record := bucket.Get(loginLimitKey); record != nil {
			var saved struct {
				Accounts          map[string]managerLoginLimit `json:"accounts"`
				managerLoginLimit                              // Older builds stored a single service-wide limit.
			}
			if err := json.Unmarshal(record, &saved); err != nil {
				return err
			}
			now := time.Now()
			for username := range a.accounts {
				limit, exists := saved.Accounts[username]
				if !exists && now.Before(saved.BlockedUntil) {
					limit = saved.managerLoginLimit
				}
				if now.Before(limit.BlockedUntil) || now.Before(limit.WindowUntil) {
					a.loginLimits[username] = limit
				}
			}
		}
		return nil
	})
}

func validManagerUsername(username string) bool {
	return username != "" && len(username) <= 64 && !strings.ContainsFunc(username, unicode.IsControl)
}

func managerPasswordDigest(password string, salt []byte) []byte {
	// SHA-256 with a 128-bit salt and a 256-bit result satisfies Key's bounds.
	digest, err := pbkdf2.Key(sha256.New, password, salt, passwordRounds, sha256.Size)
	if err != nil {
		panic(err)
	}
	return digest
}

type managerCredentials struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	InviteCode string `json:"invite_code,omitempty"`
}

func (a *managerAuth) registerAccount(input managerCredentials) error {
	account := managerAccount{Username: input.Username, Salt: make([]byte, 16)}
	rand.Read(account.Salt)
	account.Digest = managerPasswordDigest(input.Password, account.Salt)
	record, err := json.Marshal(account)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(input.InviteCode))
	if err := a.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(authBucket)
		if err != nil {
			return err
		}
		used, err := bucket.CreateBucketIfNotExists(usedInvitesBucket)
		if err != nil {
			return err
		}
		if used.Get(digest[:]) != nil {
			return errInviteInvalid
		}
		accounts, err := bucket.CreateBucketIfNotExists(accountsBucket)
		if err != nil {
			return err
		}
		if err := accounts.Put([]byte(account.Username), record); err != nil {
			return err
		}
		return used.Put(digest[:], []byte{1})
	}); err != nil {
		return err
	}
	a.accounts[account.Username] = account
	return nil
}

type managerAuthStatus struct {
	Authenticated bool `json:"authenticated"`
}

func sessionKey(r *http.Request, name string) [sha256.Size]byte {
	if cookie, err := r.Cookie(name); err == nil {
		return sha256.Sum256([]byte(cookie.Value))
	}
	return [sha256.Size]byte{}
}

func (a *managerAuth) startSession(w http.ResponseWriter, r *http.Request, name string, now time.Time) {
	previous := sessionKey(r, name)
	token := rand.Text()
	a.sessionMu.Lock()
	delete(a.sessions, previous)
	var earliest time.Time
	var oldest [sha256.Size]byte
	for key, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, key)
		} else if earliest.IsZero() || expires.Before(earliest) {
			earliest, oldest = expires, key
		}
	}
	if len(a.sessions) >= 32 {
		delete(a.sessions, oldest)
	}
	a.sessions[sha256.Sum256([]byte(token))] = now.Add(sessionLifetime)
	a.sessionMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: token, Path: "/", MaxAge: int(sessionLifetime / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

func (a *managerAuth) persistLoginLimit() error {
	return a.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(authBucket)
		if err != nil {
			return err
		}
		if len(a.loginLimits) == 0 {
			return bucket.Delete(loginLimitKey)
		}
		record, err := json.Marshal(struct {
			Accounts map[string]managerLoginLimit `json:"accounts"`
		}{Accounts: a.loginLimits})
		if err != nil {
			return err
		}
		return bucket.Put(loginLimitKey, record)
	})
}

func limitManagerAttempts(w http.ResponseWriter, limit managerLoginLimit, now time.Time) bool {
	if now.Before(limit.BlockedUntil) {
		seconds := (limit.BlockedUntil.Sub(now) + time.Second - 1) / time.Second
		w.Header().Set("Retry-After", strconv.FormatInt(int64(seconds), 10))
		problem(w, http.StatusTooManyRequests, errors.New("尝试次数过多，请稍后重试"))
		return true
	}
	return false
}

func (a *managerAuth) login(w http.ResponseWriter, input managerCredentials, now time.Time) bool {
	account, exists := a.accounts[input.Username]
	limit := a.loginLimits[input.Username]
	var anonymous *managerLoginLimit
	if !exists {
		// Bound memory use even when requests continually invent usernames.
		anonymous = &a.anonymousLimits[maphash.String(a.anonymousSeed, input.Username)%uint64(len(a.anonymousLimits))]
		limit = *anonymous
	}
	if limitManagerAttempts(w, limit, now) {
		return false
	}
	if !exists {
		// Keep the work done for an unknown username comparable to a wrong password.
		account.Salt = make([]byte, 16)
		account.Digest = make([]byte, sha256.Size)
	}
	digest := managerPasswordDigest(input.Password, account.Salt)
	valid := subtle.ConstantTimeCompare(account.Digest, digest) == 1 && exists
	switch {
	case !exists:
		anonymous.recordFailure(now, loginCooldown, loginMaxFailures)
	case !valid:
		limit.recordFailure(now, loginCooldown, loginMaxFailures)
		a.loginLimits[input.Username] = limit
	case limit.Failures > 0:
		delete(a.loginLimits, input.Username)
	}
	if exists && (!valid || limit.Failures > 0) {
		if err := a.persistLoginLimit(); err != nil {
			log.Printf("保存管理登录限制失败：%v", err)
			problem(w, http.StatusInternalServerError, errors.New("无法处理登录请求"))
			return false
		}
	}
	if !valid {
		problem(w, http.StatusUnauthorized, errors.New("账号或密码错误"))
	}
	return valid
}

func (a *managerAuth) register(w http.ResponseWriter, input managerCredentials, now time.Time) bool {
	if utf8.RuneCountInString(input.Password) < 8 || strings.TrimSpace(input.Password) == "" {
		problem(w, http.StatusBadRequest, errors.New("密码至少需要 8 个字符"))
		return false
	}
	input.InviteCode = strings.TrimSpace(input.InviteCode)
	if !validManagerInvite(a.inviteKey, input.Username, input.InviteCode, now) {
		if !limitManagerAttempts(w, a.registrationLimit, now) {
			a.registrationLimit.recordFailure(now, time.Minute, 10)
			problem(w, http.StatusUnauthorized, errInviteInvalid)
		}
		return false
	}
	if _, exists := a.accounts[input.Username]; exists {
		problem(w, http.StatusConflict, errors.New("账号已注册，请登录"))
		return false
	}
	if err := a.registerAccount(input); err != nil {
		if errors.Is(err, errInviteInvalid) {
			problem(w, http.StatusUnauthorized, errInviteInvalid)
		} else {
			log.Printf("保存管理账号失败：%v", err)
			problem(w, http.StatusInternalServerError, errors.New("无法保存管理账号"))
		}
		return false
	}
	return true
}

func (a *managerAuth) credentials(w http.ResponseWriter, r *http.Request, name string) {
	var input managerCredentials
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if !decode(w, r, &input) {
		return
	}
	input.Username = strings.TrimSpace(input.Username)
	if !validManagerUsername(input.Username) || input.Password == "" || len(input.Password) > maxPasswordBytes {
		problem(w, http.StatusBadRequest, errors.New("账号或密码格式无效"))
		return
	}

	select {
	case a.credentialSlot <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() { <-a.credentialSlot }()
	if r.Context().Err() != nil {
		return
	}
	now := time.Now()
	if r.URL.Path == "/api/auth/register" {
		if !a.register(w, input, now) {
			return
		}
	} else if !a.login(w, input, now) {
		return
	}
	a.startSession(w, r, name, time.Now())
	respond(w, managerAuthStatus{Authenticated: true})
}

func (a *managerAuth) authenticated(r *http.Request, cookieName string) bool {
	key := sessionKey(r, cookieName)
	a.sessionMu.Lock()
	expires := a.sessions[key]
	a.sessionMu.Unlock()
	return time.Now().Before(expires)
}

func (a *managerAuth) allow(w http.ResponseWriter, r *http.Request, cookieName string) bool {
	switch r.URL.Path {
	case "/api/auth/status":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return false
		}
		respond(w, managerAuthStatus{Authenticated: a.authenticated(r, cookieName)})
		return false
	case "/api/auth/register", "/api/auth/login":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return false
		}
		media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if media != "application/json" {
			problem(w, http.StatusUnsupportedMediaType, errors.New("需要 JSON 请求"))
			return false
		}
		a.credentials(w, r, cookieName)
		return false
	}

	clean := path.Clean("/" + r.URL.Path)
	if clean == "/api/auth" || strings.HasPrefix(clean, "/api/auth/") {
		problem(w, http.StatusNotFound, errors.New("接口不存在"))
		return false
	}
	if clean != "/api" && !strings.HasPrefix(clean, "/api/") {
		return true
	}
	valid := a.authenticated(r, cookieName)
	if !valid {
		problem(w, http.StatusUnauthorized, errors.New("请登录管理服务"))
	}
	return valid
}
