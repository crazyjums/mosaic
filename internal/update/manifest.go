package update

import (
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Asset 是某个平台的安装包。
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest 是发布清单。
type Manifest struct {
	Version string `json:"version"`
	Notes   string `json:"notes,omitempty"`
	PubDate string `json:"pub_date,omitempty"`
	// Page 是发布页地址：不能自动更新的平台（macOS）引导用户去这里下载。
	Page string `json:"page,omitempty"`
	// Platforms 的键是 "<GOOS>-<GOARCH>"，例如 windows-amd64、linux-amd64；macOS 用 darwin-universal。
	Platforms map[string]Asset `json:"platforms"`
}

//go:embed pubkey.txt
var embeddedPubKey string

// ErrNotConfigured 表示这个构建没有内置更新公钥，更新功能不可用。
var ErrNotConfigured = errors.New("这个版本没有配置更新签名公钥，无法检查更新")

// ParsePublicKey 解析 base64 的 ed25519 公钥。
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrNotConfigured
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("更新公钥格式不正确（应为 base64 编码的 %d 字节）", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(b), nil
}

// Verify 验证清单字节的签名（sig 是 base64 文本）。
func Verify(pub ed25519.PublicKey, manifest, sig []byte) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(pub, manifest, raw) {
		return errors.New("更新清单的签名校验失败，已忽略这次更新")
	}
	return nil
}

// GenerateKey 生成一对密钥，返回 base64 的公钥和私钥。只在发布侧（cmd/mkmanifest genkey）使用。
func GenerateKey() (pub, priv string, err error) {
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(p), base64.StdEncoding.EncodeToString(k), nil
}

// Sign 用 base64 的私钥签名，返回 base64 的签名。
func Sign(privB64 string, manifest []byte) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("签名私钥格式不正确")
	}
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(b), manifest))), nil
}

func parseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("更新清单不是合法的 JSON: %w", err)
	}
	if _, _, err := parseVersion(m.Version); err != nil {
		return nil, err
	}
	return &m, nil
}
