package siwc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Endpoints 是授权服务器的地址。默认值来自 OpenAI 的文档；测试时换成本地的假服务器。
type Endpoints struct {
	Issuer       string
	AuthorizeURL string
	TokenURL     string
	DiscoveryURL string
	// Resource 是令牌的使用对象，授权、换令牌、刷新时都要带上。
	Resource string
}

// DefaultEndpoints 是 OpenAI 的正式地址。
var DefaultEndpoints = Endpoints{
	Issuer:       "https://auth.openai.com",
	AuthorizeURL: "https://auth.openai.com/api/accounts/authorize",
	TokenURL:     "https://auth.openai.com/api/accounts/oauth/token",
	DiscoveryURL: "https://auth.openai.com/.well-known/openid-configuration",
	Resource:     "https://api.openai.com/v1",
}

const (
	// Scope 中的 chatgpt.tokens.use.direct 是"使用套餐额度"的权限，offline_access 用于拿刷新令牌。
	Scope = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	// DynamicClient 是首次注册的入口，不是 client_id，不能保存，也不能用来换令牌。
	DynamicClient = "dynamic_agent_client"
	usePlanScope  = "chatgpt.tokens.use.direct"
)

// randomString 返回 n 字节随机数的 base64url 编码，用作 PKCE verifier、state、nonce。
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败意味着系统出了大问题
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// tokenResponse 是令牌端点的响应。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	// 文档列出了这个字段但没规定格式：Unix 秒、毫秒、ISO 8601 都接受。
	EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at"`
}

// oauthError 是令牌端点的错误响应（RFC 6749 §5.2）。
type oauthError struct {
	Status      int
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("oauth: %s: %s (HTTP %d)", e.Code, e.Description, e.Status)
	}
	return fmt.Sprintf("oauth: %s (HTTP %d)", e.Code, e.Status)
}

// isInvalidGrant 表示刷新令牌已经失效（过期、被吊销或已被使用），只能重新登录。
func isInvalidGrant(err error) bool {
	var oe *oauthError
	return errors.As(err, &oe) && oe.Code == "invalid_grant"
}

// postForm 向令牌端点发表单请求。不跟随重定向：令牌端点返回重定向说明配置有误，跟过去可能把授权码发到别处。
func postForm(ctx context.Context, client *http.Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		oe := &oauthError{Status: resp.StatusCode}
		if json.Unmarshal(b, oe) != nil || oe.Code == "" {
			oe.Code = "http_error"
			oe.Description = strings.TrimSpace(string(b))
			if len(oe.Description) > 300 {
				oe.Description = oe.Description[:300]
			}
		}
		return oe
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("oauth: decode token response: %w", err)
	}
	return nil
}

func noRedirectClient(base *http.Client) *http.Client {
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// parseTime 解析 earliest_refresh_at（Unix 秒 / 毫秒 / ISO 8601）。解析不了返回零值，表示"随时可以刷新"。
func parseTime(raw json.RawMessage) time.Time {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return time.Time{}
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		if n > 1e12 { // 毫秒
			return time.UnixMilli(int64(n))
		}
		return time.Unix(int64(n), 0)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	return time.Time{}
}

// ---- 发现文档与 ID 令牌校验 ----

type discovery struct {
	Issuer             string `json:"issuer"`
	JWKSURI            string `json:"jwks_uri"`
	RevocationEndpoint string `json:"revocation_endpoint"`
}

func getJSON(ctx context.Context, client *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// sameOrigin 检查发现文档里的地址和签发者是同一个站点，防止被篡改的发现文档把我们引到别处。
func sameOrigin(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	return err1 == nil && err2 == nil && ua.Scheme == ub.Scheme && ua.Host == ub.Host
}

func fetchDiscovery(ctx context.Context, client *http.Client, ep Endpoints) (*discovery, error) {
	var d discovery
	if err := getJSON(ctx, client, ep.DiscoveryURL, &d); err != nil {
		return nil, fmt.Errorf("siwc: discovery: %w", err)
	}
	if d.Issuer != ep.Issuer {
		return nil, fmt.Errorf("siwc: discovery issuer %q does not match %q", d.Issuer, ep.Issuer)
	}
	if !sameOrigin(d.JWKSURI, ep.Issuer) || (d.RevocationEndpoint != "" && !sameOrigin(d.RevocationEndpoint, ep.Issuer)) {
		return nil, errors.New("siwc: discovery endpoints are not on the issuer's origin")
	}
	return &d, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// idClaims 是 ID 令牌里用到的字段。aud 可能是字符串或数组。
type idClaims struct {
	Iss   string          `json:"iss"`
	Sub   string          `json:"sub"`
	Aud   json.RawMessage `json:"aud"`
	Azp   string          `json:"azp"`
	Exp   int64           `json:"exp"`
	Iat   int64           `json:"iat"`
	Nonce string          `json:"nonce"`
	Email string          `json:"email"`
	// 套餐类型等信息在命名空间声明里（与 Codex 相同的命名空间）；拿不到也不影响使用。
	Auth *struct {
		PlanType string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
	Profile *struct {
		Email string `json:"email"`
	} `json:"https://api.openai.com/profile"`
}

func (c idClaims) audiences() []string {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(c.Aud, &many)
	return many
}

func (c idClaims) email() string {
	if c.Email == "" && c.Profile != nil {
		return c.Profile.Email
	}
	return c.Email
}

func (c idClaims) plan() string {
	if c.Auth != nil {
		return c.Auth.PlanType
	}
	return ""
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// verifyIDToken 校验 ID 令牌：RS256 签名（用发现文档里的 JWKS）、签发者、受众（必须是签发给本应用的 client_id）、
// 有效期和 nonce。nonce 为空表示不检查（刷新时的新 ID 令牌没有 nonce）。
//
// 注意：ID 令牌只证明"是谁登录的"，能不能用套餐额度要看访问令牌的 scope。
func verifyIDToken(ctx context.Context, client *http.Client, d *discovery, token, clientID, nonce string, now time.Time) (*idClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("siwc: malformed id_token")
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || json.Unmarshal(hb, &header) != nil {
		return nil, errors.New("siwc: malformed id_token header")
	}
	if header.Alg != "RS256" {
		return nil, fmt.Errorf("siwc: unexpected id_token alg %q", header.Alg)
	}

	// 签名。
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, client, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("siwc: jwks: %w", err)
	}
	var key *rsa.PublicKey
	for _, k := range set.Keys {
		if k.Kty == "RSA" && k.Kid == header.Kid {
			key, err = rsaKey(k)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	if key == nil {
		return nil, fmt.Errorf("siwc: no signing key %q in jwks", header.Kid)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("siwc: malformed id_token signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, errors.New("siwc: id_token signature is invalid")
	}

	// 声明。
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("siwc: malformed id_token payload")
	}
	var c idClaims
	if err := json.Unmarshal(pb, &c); err != nil {
		return nil, errors.New("siwc: malformed id_token claims")
	}
	const skew = 2 * time.Minute
	audOK := false
	for _, a := range c.audiences() {
		audOK = audOK || eq(a, clientID)
	}
	switch {
	case !eq(c.Iss, d.Issuer):
		return nil, errors.New("siwc: id_token issuer mismatch")
	case !audOK:
		return nil, errors.New("siwc: id_token audience mismatch")
	case c.Azp != "" && !eq(c.Azp, clientID):
		return nil, errors.New("siwc: id_token azp mismatch")
	case time.Unix(c.Exp, 0).Before(now.Add(-skew)):
		return nil, errors.New("siwc: id_token expired")
	case time.Unix(c.Iat, 0).After(now.Add(skew)):
		return nil, errors.New("siwc: id_token issued in the future")
	case nonce != "" && !eq(c.Nonce, nonce):
		return nil, errors.New("siwc: id_token nonce mismatch")
	case c.Sub == "":
		return nil, errors.New("siwc: id_token has no subject")
	}
	return &c, nil
}

func rsaKey(k jwk) (*rsa.PublicKey, error) {
	nb, err1 := base64.RawURLEncoding.DecodeString(k.N)
	eb, err2 := base64.RawURLEncoding.DecodeString(k.E)
	if err1 != nil || err2 != nil || len(eb) == 0 || len(eb) > 4 {
		return nil, errors.New("siwc: malformed jwk")
	}
	e := int(new(big.Int).SetBytes(eb).Int64())
	// 拒绝不安全的公钥指数（过小或为偶数）。
	if e < 3 || e%2 == 0 {
		return nil, errors.New("siwc: insecure jwk exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}
