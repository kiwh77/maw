package localauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Cookie 与请求头名字，与契约 §7.1 一致。
const (
	SessionCookieName = "maw_session"
	CSRFCookieName    = "maw_csrf"
	CSRFHeaderName    = "X-CSRF-Token"
)

var (
	// ErrNoSession 请求没有会话 Cookie。
	ErrNoSession = errors.New("localauth: 无会话")
	// ErrSessionInvalid 会话 Cookie 被改过或已过期。
	ErrSessionInvalid = errors.New("localauth: 会话无效")
	// ErrNonLoopbackHost 非 loopback 主机不允许下发会话。
	ErrNonLoopbackHost = errors.New("localauth: 会话只能下发给本机主机")
	// ErrOriginMissing 非安全方法缺少 Origin。
	ErrOriginMissing = errors.New("localauth: 缺少 Origin")
	// ErrOriginMismatch Origin 与 Host 不同源。
	ErrOriginMismatch = errors.New("localauth: 跨源请求")
	// ErrCSRFMismatch 双提交不一致。
	ErrCSRFMismatch = errors.New("localauth: CSRF 校验失败")
)

// Session 是已通过签名校验的会话。
type Session struct {
	ID        string
	ExpiresAt time.Time
	csrf      string
}

// CSRFToken 返回与该会话绑定的 CSRF 值；同时下发到可读 Cookie 与请求头。
func (s Session) CSRFToken() string { return s.csrf }

// SessionManager 负责会话的签发与校验。
//
// 会话是 HMAC 签名的无状态 Cookie：内容只有随机会话 ID 与到期时间，
// 因此不引入第二张表；撤销会话的手段是轮换 key（更强隔离不在本契约范围）。
type SessionManager struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewSessionManager 创建会话管理器；key 至少 32 字节。
func NewSessionManager(key []byte, ttl time.Duration) (*SessionManager, error) {
	if len(key) < 32 {
		return nil, errors.New("localauth: 会话签名 key 至少 32 字节")
	}
	if ttl <= 0 {
		return nil, errors.New("localauth: 会话有效期必须为正")
	}
	return &SessionManager{key: append([]byte(nil), key...), ttl: ttl, now: time.Now}, nil
}

// Issue 下发会话与 CSRF Cookie，返回会话对象。
//
// 只允许 loopback 主机：契约 §7.1「仅 loopback 主机下发」。
func (m *SessionManager) Issue(w http.ResponseWriter, host string) (Session, error) {
	if !IsLoopbackHost(host) {
		return Session{}, ErrNonLoopbackHost
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return Session{}, fmt.Errorf("localauth: 生成会话失败: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(buf)
	exp := m.now().Add(m.ttl)
	value := id + "." + strconv.FormatInt(exp.Unix(), 10) + "." + m.sign(id, exp.Unix())
	csrf := m.csrfFor(id)

	secure := false // loopback 默认 http；部署在 https 反代后由调用方加 Secure
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookieName, Value: value, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: secure, MaxAge: int(m.ttl.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookieName, Value: csrf, Path: "/", HttpOnly: false,
		SameSite: http.SameSiteLaxMode, Secure: secure, MaxAge: int(m.ttl.Seconds()),
	})
	return Session{ID: id, ExpiresAt: exp, csrf: csrf}, nil
}

// Authenticate 校验请求里的会话 Cookie。
func (m *SessionManager) Authenticate(r *http.Request) (Session, error) {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return Session{}, ErrNoSession
	}
	parts := strings.Split(c.Value, ".")
	if len(parts) != 3 {
		return Session{}, ErrSessionInvalid
	}
	id, expRaw, mac := parts[0], parts[1], parts[2]
	exp, err := strconv.ParseInt(expRaw, 10, 64)
	if err != nil {
		return Session{}, ErrSessionInvalid
	}
	want := m.sign(id, exp)
	if subtle.ConstantTimeCompare([]byte(mac), []byte(want)) != 1 {
		return Session{}, ErrSessionInvalid
	}
	expires := time.Unix(exp, 0)
	if !m.now().Before(expires) {
		return Session{}, ErrSessionInvalid
	}
	return Session{ID: id, ExpiresAt: expires, csrf: m.csrfFor(id)}, nil
}

// VerifyCSRF 校验双提交：请求头必须等于可读 Cookie，且等于会话派生值。
// 只做「头 == Cookie」不够：攻击者若能写 Cookie（同站子域），派生值能兜住。
func (m *SessionManager) VerifyCSRF(r *http.Request, sess Session) error {
	header := r.Header.Get(CSRFHeaderName)
	c, err := r.Cookie(CSRFCookieName)
	if err != nil || c.Value == "" || header == "" {
		return ErrCSRFMismatch
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(c.Value)) != 1 {
		return ErrCSRFMismatch
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(sess.csrf)) != 1 {
		return ErrCSRFMismatch
	}
	return nil
}

// Clear 清除会话与 CSRF Cookie。
func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: CSRFCookieName, Value: "", Path: "/", HttpOnly: false,
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (m *SessionManager) sign(id string, exp int64) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte("sess|" + id + "|" + strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *SessionManager) csrfFor(id string) string {
	mac := hmac.New(sha256.New, m.key)
	mac.Write([]byte("csrf|" + id))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// IsLoopbackHost 报告 Host（可带端口、可为 IPv6）是否指向本机。
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// CheckSameOrigin 校验非安全方法的 Origin 必须与 Host 同源。
// 缺 Origin、Origin=null、scheme 或 host 不同都拒绝（契约 §7.1）。
//
// scheme 取本请求的实际协议：loopback 默认 http，TLS 直连为 https。
// 反向代理终止 TLS 的场景不在本契约范围内（§10），届时由调用方补齐可信 scheme。
func CheckSameOrigin(r *http.Request) error {
	raw := strings.TrimSpace(r.Header.Get("Origin"))
	if raw == "" {
		return ErrOriginMissing
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return ErrOriginMismatch
	}
	wantScheme := "http"
	if r.TLS != nil {
		wantScheme = "https"
	}
	if !strings.EqualFold(u.Scheme, wantScheme) || !strings.EqualFold(u.Host, r.Host) {
		return ErrOriginMismatch
	}
	return nil
}
