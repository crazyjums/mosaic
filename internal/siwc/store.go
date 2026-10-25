package siwc

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 本地存储（都在数据目录下的 chatgpt/ 子目录，目录 0700、文件 0600）：
//
//	host-id            本机的 ext_agent_host_id，生成一次后不再变化
//	registration.json  签发的 client_id 和对应的账号；退出登录后保留，下次登录直接复用
//	credential.json    令牌；退出登录时删除
//	lock               刷新令牌时的跨进程锁

// Credential 是登录后保存的令牌。令牌只在后端使用，不会通过任何绑定方法发给前端。
type Credential struct {
	Issuer       string    `json:"issuer"`
	Subject      string    `json:"subject"`
	Email        string    `json:"email,omitempty"`
	Plan         string    `json:"plan,omitempty"`
	ClientID     string    `json:"client_id"`
	HostID       string    `json:"ext_agent_host_id"`
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Scopes       string    `json:"scopes,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	// EarliestRefreshAt 来自令牌响应的 earliest_refresh_at：在此之前（且令牌没过期）不要刷新。
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitzero"`
	SavedAt           time.Time `json:"saved_at"`
}

// Registration 是 OpenAI 为本应用签发的客户端注册。
type Registration struct {
	ClientID string `json:"client_id"`
	Subject  string `json:"subject,omitempty"`
	Email    string `json:"email,omitempty"`
}

type store struct{ dir string }

func (s store) path(name string) string { return filepath.Join(s.dir, name) }

func (s store) readJSON(name string, v any) error {
	b, err := os.ReadFile(s.path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// writeJSON 原子写入：先写临时文件再重命名，写到一半崩溃也不会留下损坏的令牌文件。
func (s store) writeJSON(name string, v any) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, name+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(name))
}

func (s store) credential() (*Credential, error) {
	var c Credential
	if err := s.readJSON("credential.json", &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s store) saveCredential(c *Credential) error { return s.writeJSON("credential.json", c) }

func (s store) deleteCredential() error {
	err := os.Remove(s.path("credential.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s store) registration() Registration {
	var r Registration
	_ = s.readJSON("registration.json", &r)
	return r
}

func (s store) saveRegistration(r Registration) error { return s.writeJSON("registration.json", r) }

// hostID 返回本机的 ext_agent_host_id，没有就生成一个（urn:uuid:<uuid4>）。
func (s store) hostID() (string, error) {
	if b, err := os.ReadFile(s.path("host-id")); err == nil && len(b) > 0 {
		return string(b), nil
	}
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return "", err
	}
	u[6] = u[6]&0x0f | 0x40 // 版本 4
	u[8] = u[8]&0x3f | 0x80 // RFC 4122 变体
	id := fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.path("host-id"), []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// withLock 在跨进程锁内执行 fn。
//
// 刷新令牌会轮换：两个进程（例如桌面端和 CLI）同时拿同一个刷新令牌去刷新，后到的那个会失败，
// 甚至可能让整个登录失效。所以刷新前要拿锁，并在锁内重新读一次文件——也许另一个进程刚刚刷新过。
//
// 用"独占创建锁文件"而不是 flock：标准库没有跨平台的文件锁，而锁文件在 Windows 上同样可用。
// 进程崩溃会留下锁文件，所以超过 staleLock 的锁视为过期。
func (s store) withLock(fn func() error) error {
	const (
		staleLock = 30 * time.Second
		waitMax   = 15 * time.Second
	)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	lock := s.path("lock")
	deadline := time.Now().Add(waitMax)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return errors.New("siwc: timed out waiting for the token lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer os.Remove(lock)
	return fn()
}
