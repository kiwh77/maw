package localauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type probe struct {
	called bool
	id     Identity
}

func probeHandler(p *probe) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.called = true
		p.id, _ = IdentityFrom(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func newAuth(t *testing.T) (*Authenticator, *TokenStore, *SessionManager) {
	t.Helper()
	store := newStore(t)
	sessions := newSessionManager(t)
	return &Authenticator{Tokens: store, Sessions: sessions}, store, sessions
}

func TestBearerTokenAuthenticates(t *testing.T) {
	auth, store, _ := newAuth(t)
	plain, _, err := store.Mint("agent-1", ScopeRead)
	if err != nil {
		t.Fatalf("签发失败：%v", err)
	}
	p := &probe{}
	srv := auth.Middleware(probeHandler(p))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !p.called {
		t.Fatalf("有效令牌应通过：%d", rec.Code)
	}
	if p.id.Via != "token" || p.id.Actor != "agent-1" || !p.id.HasScope(ScopeRead) {
		t.Fatalf("身份未落到 context：%+v", p.id)
	}
}

func TestTokenInQueryIsRejected(t *testing.T) {
	auth, store, _ := newAuth(t)
	plain, _, _ := store.Mint("agent-1", ScopeRead)
	p := &probe{}
	srv := auth.Middleware(probeHandler(p))

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board?access_token="+plain, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || p.called {
		t.Fatalf("URL 里的令牌不得生效：%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), plain) {
		t.Fatal("错误体不得回显令牌")
	}
}

func TestTokenInBodyIsIgnored(t *testing.T) {
	auth, store, _ := newAuth(t)
	plain, _, _ := store.Mint("agent-1", ScopeRead)
	p := &probe{}
	srv := auth.Middleware(probeHandler(p))

	body := `{"token":"` + plain + `"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || p.called {
		t.Fatalf("body 里的令牌不得被认：%d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), plain) {
		t.Fatal("错误体不得回显令牌")
	}
}

func TestRevokedTokenRejectedImmediately(t *testing.T) {
	auth, store, _ := newAuth(t)
	plain, rec0, _ := store.Mint("agent-1", ScopeRead)
	if err := store.Revoke(rec0.ID); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	p := &probe{}
	srv := auth.Middleware(probeHandler(p))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("撤销后必须 401，得到 %d", rec.Code)
	}
}

func TestMissingCredentialsIs401(t *testing.T) {
	auth, _, _ := newAuth(t)
	srv := auth.Middleware(probeHandler(&probe{}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭据必须 401，得到 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("错误码必须是 unauthenticated：%s", rec.Body.String())
	}
}

func TestScopeEnforcement(t *testing.T) {
	auth, store, _ := newAuth(t)
	readToken, _, _ := store.Mint("reader", ScopeRead)
	writeToken, _, _ := store.Mint("writer", ScopeWrite)

	p := &probe{}
	writeOnly := auth.RequireScope(ScopeWrite, probeHandler(p))

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", nil)
	req.Header.Set("Authorization", "Bearer "+readToken)
	rec := httptest.NewRecorder()
	auth.Middleware(writeOnly).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_scope") {
		t.Fatalf("read 令牌做写必须 403 insufficient_scope：%d %s", rec.Code, rec.Body.String())
	}
	if p.called {
		t.Fatal("作用域不足时不得进入 handler")
	}

	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/claims", nil)
	req2.Header.Set("Authorization", "Bearer "+writeToken)
	rec2 := httptest.NewRecorder()
	auth.Middleware(writeOnly).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("write 令牌做写应通过：%d", rec2.Code)
	}
}

func TestSessionReadNeedsNoCSRF(t *testing.T) {
	auth, _, sessions := newAuth(t)
	p := &probe{}
	srv := auth.Middleware(probeHandler(p))

	host := "127.0.0.1:8030"
	issue := httptest.NewRecorder()
	if _, err := sessions.Issue(issue, host); err != nil {
		t.Fatalf("下发会话失败：%v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/board", nil)
	for _, c := range issue.Result().Cookies() {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || p.id.Via != "session" {
		t.Fatalf("只读会话应通过：%d %+v", rec.Code, p.id)
	}
	if p.id.HasScope(ScopeAdmin) {
		t.Fatal("浏览器会话不得带 admin")
	}
}

func sessionRequest(t *testing.T, sessions *SessionManager, host, method, path string, withOrigin, withCSRF bool) *http.Request {
	t.Helper()
	issue := httptest.NewRecorder()
	sess, err := sessions.Issue(issue, host)
	if err != nil {
		t.Fatalf("下发会话失败：%v", err)
	}
	req := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(`{}`))
	for _, c := range issue.Result().Cookies() {
		req.AddCookie(c)
	}
	if withOrigin {
		req.Header.Set("Origin", "http://"+host)
	}
	if withCSRF {
		req.Header.Set(CSRFHeaderName, sess.CSRFToken())
	}
	return req
}

func TestSessionWriteRequiresOriginAndCSRF(t *testing.T) {
	auth, _, sessions := newAuth(t)
	host := "127.0.0.1:8030"
	srv := auth.Middleware(probeHandler(&probe{}))

	cases := []struct {
		name       string
		origin     bool
		csrf       bool
		wantStatus int
		wantCode   string
	}{
		{"缺 Origin", false, true, http.StatusForbidden, "origin_mismatch"},
		{"缺 CSRF", true, false, http.StatusForbidden, "csrf_failed"},
		{"齐备", true, true, http.StatusOK, ""},
	}
	for _, c := range cases {
		req := sessionRequest(t, sessions, host, http.MethodPost, "/api/v1/projects/p/claims", c.origin, c.csrf)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != c.wantStatus {
			t.Errorf("%s：want %d got %d", c.name, c.wantStatus, rec.Code)
		}
		if c.wantCode != "" && !strings.Contains(rec.Body.String(), c.wantCode) {
			t.Errorf("%s：错误码应为 %s，body=%s", c.name, c.wantCode, rec.Body.String())
		}
	}
}

func TestSessionWriteRejectsCrossOrigin(t *testing.T) {
	auth, _, sessions := newAuth(t)
	host := "127.0.0.1:8030"
	req := sessionRequest(t, sessions, host, http.MethodPost, "/api/v1/projects/p/claims", false, true)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "origin_mismatch") {
		t.Fatalf("跨源写必须 403 origin_mismatch：%d %s", rec.Code, rec.Body.String())
	}
}

func TestSessionRequiresLoopbackHost(t *testing.T) {
	auth, _, sessions := newAuth(t)
	// 会话是从本机签发的，但请求 Host 被改成外部域名（DNS rebinding 场景）。
	req := sessionRequest(t, sessions, "127.0.0.1:8030", http.MethodGet, "/api/v1/board", true, true)
	req.Host = "rebind.example.com"
	rec := httptest.NewRecorder()
	auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "loopback_required") {
		t.Fatalf("非本机 Host 必须 403 loopback_required：%d %s", rec.Code, rec.Body.String())
	}
}

func TestTokenSkipsCSRF(t *testing.T) {
	auth, store, _ := newAuth(t)
	plain, _, _ := store.Mint("agent-1", ScopeWrite)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/projects/p/claims", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("CLI 令牌不需要 CSRF（非浏览器路径）：%d", rec.Code)
	}
}

func TestBadAuthorizationHeaderIs401(t *testing.T) {
	auth, _, _ := newAuth(t)
	for _, header := range []string{"Basic abc", "Bearer", "Bearer  ", "Bearer a b"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%q 应 401，得到 %d", header, rec.Code)
		}
	}
}

func TestNoStoreHeaderOnAuthErrors(t *testing.T) {
	auth, _, _ := newAuth(t)
	rec := httptest.NewRecorder()
	auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/board", nil))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("鉴权失败响应不得被缓存")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("错误体必须是 JSON：%s", ct)
	}
}

func TestExpiredSessionIs401(t *testing.T) {
	auth, _, sessions := newAuth(t)
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	sessions.now = func() time.Time { return base }
	req := sessionRequest(t, sessions, "127.0.0.1:8030", http.MethodGet, "/api/v1/board", true, false)
	sessions.now = func() time.Time { return base.Add(3 * time.Hour) }
	rec := httptest.NewRecorder()
	auth.Middleware(probeHandler(&probe{})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("过期会话必须 401，得到 %d", rec.Code)
	}
}
