package localauth

import (
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("生成 key 失败：%v", err)
	}
	return key
}

func newSessionManager(t *testing.T) *SessionManager {
	t.Helper()
	m, err := NewSessionManager(testKey(t), time.Hour)
	if err != nil {
		t.Fatalf("创建会话管理器失败：%v", err)
	}
	return m
}

func TestSessionManagerRequiresStrongKey(t *testing.T) {
	if _, err := NewSessionManager([]byte("short"), time.Hour); err == nil {
		t.Fatal("短 key 必须被拒")
	}
	m := newSessionManager(t)
	if _, err := NewSessionManager(testKey(t), 0); err == nil {
		_ = m
		t.Fatal("非正有效期必须被拒")
	}
}

func TestIssueOnlyToLoopback(t *testing.T) {
	m := newSessionManager(t)
	rec := httptest.NewRecorder()
	if _, err := m.Issue(rec, "example.com"); !errors.Is(err, ErrNonLoopbackHost) {
		t.Fatalf("非 loopback 主机不得下发会话，得到 %v", err)
	}
	for _, host := range []string{"127.0.0.1:8030", "localhost:8030", "[::1]:8030", "localhost"} {
		if !IsLoopbackHost(host) {
			t.Errorf("%s 应判为本机", host)
		}
	}
	for _, host := range []string{"", "example.com", "10.0.0.2:80", "127.0.0.1.evil.com"} {
		if IsLoopbackHost(host) {
			t.Errorf("%s 不应判为本机", host)
		}
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	m := newSessionManager(t)
	rec := httptest.NewRecorder()
	sess, err := m.Issue(rec, "127.0.0.1:8030")
	if err != nil {
		t.Fatalf("下发失败：%v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("应下发会话与 CSRF 两个 Cookie，实际 %d", len(cookies))
	}
	byName := map[string]*http.Cookie{}
	for _, c := range cookies {
		byName[c.Name] = c
	}
	session := byName[SessionCookieName]
	if session == nil || !session.HttpOnly {
		t.Fatal("maw_session 必须 HttpOnly")
	}
	if session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatalf("会话 Cookie 属性不符：%+v", session)
	}
	csrf := byName[CSRFCookieName]
	if csrf == nil {
		t.Fatal("必须下发 maw_csrf")
	}
	if csrf.HttpOnly {
		t.Fatal("maw_csrf 必须可读（双提交要靠 JS 读取）")
	}
	if csrf.Value != sess.CSRFToken() {
		t.Fatal("CSRF Cookie 与派生值必须一致")
	}
}

func TestAuthenticateAndExpire(t *testing.T) {
	m := newSessionManager(t)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base }

	rec := httptest.NewRecorder()
	sess, err := m.Issue(rec, "127.0.0.1:8030")
	if err != nil {
		t.Fatalf("下发失败：%v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	got, err := m.Authenticate(req)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("会话校验失败：%+v %v", got, err)
	}

	// 篡改签名。
	bad := *req
	bad.Header = req.Header.Clone()
	bad.Header.Set("Cookie", SessionCookieName+"="+sess.ID+".1.deadbeef")
	if _, err := m.Authenticate(&bad); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("被改过的 Cookie 必须拒绝，得到 %v", err)
	}

	// 过期。
	m.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := m.Authenticate(req); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("过期会话必须拒绝，得到 %v", err)
	}
}

func TestVerifyCSRF(t *testing.T) {
	m := newSessionManager(t)
	rec := httptest.NewRecorder()
	sess, _ := m.Issue(rec, "127.0.0.1:8030")

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", nil)
	req.Header.Set(CSRFHeaderName, sess.CSRFToken())
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: sess.CSRFToken()})
	if err := m.VerifyCSRF(req, sess); err != nil {
		t.Fatalf("一致的双提交应通过：%v", err)
	}

	req.Header.Set(CSRFHeaderName, "forged")
	if err := m.VerifyCSRF(req, sess); !errors.Is(err, ErrCSRFMismatch) {
		t.Fatalf("头与 Cookie 不一致必须拒绝，得到 %v", err)
	}

	// 只改 Cookie、头也改成同一个值：仍必须被会话派生值挡住。
	forged := "forged-value"
	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", nil)
	req2.Header.Set(CSRFHeaderName, forged)
	req2.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: forged})
	if err := m.VerifyCSRF(req2, sess); !errors.Is(err, ErrCSRFMismatch) {
		t.Fatalf("自造的一对值必须被派生值挡住，得到 %v", err)
	}
}

func TestCheckSameOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", nil)
	if err := CheckSameOrigin(req); !errors.Is(err, ErrOriginMissing) {
		t.Fatalf("缺 Origin 必须拒绝，得到 %v", err)
	}
	req.Header.Set("Origin", "http://127.0.0.1:8030")
	if err := CheckSameOrigin(req); err != nil {
		t.Fatalf("同源应通过：%v", err)
	}
	for _, bad := range []string{"http://evil.com", "null", "http://127.0.0.1:9999", "https://127.0.0.1:8030"} {
		req.Header.Set("Origin", bad)
		if err := CheckSameOrigin(req); err == nil {
			t.Errorf("Origin %q 必须被拒", bad)
		}
	}
}
