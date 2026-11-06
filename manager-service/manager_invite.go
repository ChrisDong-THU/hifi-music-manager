package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	managerInviteKeyFile = "manager-invite.key"
	managerInviteTTL     = 30 * time.Minute
	invitePayloadSize    = 8 + 16
)

func readManagerInviteKey(config string) ([]byte, error) {
	key, err := os.ReadFile(filepath.Join(config, managerInviteKeyFile))
	if err == nil && len(key) != sha256.Size {
		return nil, errors.New("邀请码签发密钥无效")
	}
	return key, err
}

// The server creates the key while holding the auth database lock. The local
// issuer only reads it, so it can generate an invitation while the server runs.
func loadManagerInviteKey(config string) ([]byte, error) {
	filename := filepath.Join(config, managerInviteKeyFile)
	key, err := readManagerInviteKey(config)
	if !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return nil, err
		}
		return key, restrictAuthFile(filename)
	}
	key = make([]byte, sha256.Size)
	rand.Read(key)
	f, err := os.CreateTemp(config, ".manager-invite-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if err := restrictAuthFile(f.Name()); err != nil {
		_ = f.Close()
		return nil, err
	}
	_, writeErr := f.Write(key)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return nil, err
	}
	return key, os.Rename(f.Name(), filename)
}

func managerInviteSignature(key, payload []byte, username string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	mac.Write([]byte(username))
	return mac.Sum(nil)
}

func newManagerInvite(key []byte, username string, now time.Time) string {
	var payload [invitePayloadSize]byte
	binary.BigEndian.PutUint64(payload[:8], uint64(now.Add(managerInviteTTL).Unix()))
	rand.Read(payload[8:])
	token := append(payload[:], managerInviteSignature(key, payload[:], username)...)
	return base64.RawURLEncoding.EncodeToString(token)
}

func validManagerInvite(key []byte, username, code string, now time.Time) bool {
	if len(code) != base64.RawURLEncoding.EncodedLen(invitePayloadSize+sha256.Size) {
		return false
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(code)
	if err != nil || len(token) != invitePayloadSize+sha256.Size {
		return false
	}
	expires := int64(binary.BigEndian.Uint64(token[:8]))
	return now.Unix() < expires && hmac.Equal(token[invitePayloadSize:], managerInviteSignature(key, token[:invitePayloadSize], username))
}

func printManagerInvite(directory, username string) error {
	username = strings.TrimSpace(username)
	if !validManagerUsername(username) {
		return errors.New("请指定有效的受邀账号")
	}
	key, err := readManagerInviteKey(filepath.Join(directory, "config"))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("未找到邀请码签发密钥，请先启动管理服务")
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, newManagerInvite(key, username, time.Now()))
	return err
}
