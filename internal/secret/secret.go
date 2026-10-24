package secret

import (
	"errors"
	"fmt"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound 表示条目不存在。
var ErrNotFound = errors.New("secret: not found")

// Store 是密钥存储。key 是条目名（例如 "provider/openai"）。
type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
	// Available 检查存储当前是否可用；不可用时返回原因。
	Available() error
}

// Service 是钥匙串条目的服务名，用户在"钥匙串访问"等工具里看到的就是它。
const Service = "myllmapp"

// Keyring 是系统钥匙串的实现。
type Keyring struct {
	once  sync.Once
	probe error
}

func NewKeyring() *Keyring { return &Keyring{} }

func (k *Keyring) Get(key string) (string, error) {
	v, err := keyring.Get(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	return v, err
}

func (k *Keyring) Set(key, value string) error { return keyring.Set(Service, key, value) }

func (k *Keyring) Delete(key string) error {
	err := keyring.Delete(Service, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// Available 写入、读回、删除一个探测条目。只探测一次：每次都探测会在某些系统上反复弹出授权框。
func (k *Keyring) Available() error {
	k.once.Do(func() {
		const probeKey = "probe"
		if err := keyring.Set(Service, probeKey, "ok"); err != nil {
			k.probe = fmt.Errorf("系统钥匙串不可用：%w", err)
			return
		}
		if v, err := keyring.Get(Service, probeKey); err != nil || v != "ok" {
			k.probe = fmt.Errorf("系统钥匙串读取失败：%v", err)
		}
		_ = keyring.Delete(Service, probeKey)
	})
	return k.probe
}

// Memory 是内存实现，供测试使用。
type Memory struct {
	mu   sync.Mutex
	m    map[string]string
	Fail error // 非空时 Available / Set 返回它，模拟钥匙串不可用
}

func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (s *Memory) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (s *Memory) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail
	}
	s.m[key] = value
	return nil
}

func (s *Memory) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

func (s *Memory) Available() error { return s.Fail }

// Len 返回条目数（测试用）。
func (s *Memory) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
