package localauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Identity 是鉴权结果的唯一载体，挂在 context 上供后续 handler 读取。
type Identity struct {
	Actor     string
	Scopes    []Scope
	Via       string // token | session
	SessionID string
}

// HasScope 报告当前身份能否满足所需作用域。
func (i Identity) HasScope(need Scope) bool {
	for _, s := range i.Scopes {
		if s.Allows(need) {
			return true
		}
	}
	return false
}

type identityKey struct{}

// IdentityFrom 取出鉴权身份；未通过中间件时返回 false。
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

func withIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// Authenticator 是鉴权中间件：先看 Bearer，再看会话；会话的非安全方法还要过 Origin 与 CSRF。
type Authenticator struct {
	Tokens   *TokenStore
	Sessions *SessionManager
}

// Middleware 包装下游 handler。
//
// 判定顺序对应契约 §7：令牌（Bearer）→ 会话 → 会话的 Origin／CSRF。
// 令牌只从 `Authorization` 读取；URL 里出现令牌直接拒绝，body 里的令牌一律不认。
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenInQuery(r) {
			// 不读、不记、不回显：只报「未认证」，避免把 URL 里的令牌写进日志或错误体。
			WriteJSONError(w, http.StatusUnauthorized, "unauthenticated",
				"令牌只能通过 Authorization: Bearer 传递", nil)
			return
		}

		if raw := r.Header.Get("Authorization"); raw != "" {
			plain, ok := parseBearer(raw)
			if !ok || a.Tokens == nil {
				WriteJSONError(w, http.StatusUnauthorized, "unauthenticated", "凭据无效", nil)
				return
			}
			tok, err := a.Tokens.Lookup(plain)
			if err != nil {
				// 形态错、查不到、已撤销一律 401：不给攻击者区分「存在但不合法」的机会。
				WriteJSONError(w, http.StatusUnauthorized, "unauthenticated", "凭据无效或已撤销", nil)
				return
			}
			id := Identity{Actor: tok.Actor, Scopes: append([]Scope(nil), tok.Scopes...), Via: "token"}
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
			return
		}

		if a.Sessions == nil {
			WriteJSONError(w, http.StatusUnauthorized, "unauthenticated", "缺少凭据", nil)
			return
		}
		sess, err := a.Sessions.Authenticate(r)
		if err != nil {
			WriteJSONError(w, http.StatusUnauthorized, "unauthenticated", "缺少或无效的会话", nil)
			return
		}
		// 会话只在 loopback 主机生效：挡住 DNS rebinding 把本机服务映射到外站域名。
		if !IsLoopbackHost(r.Host) {
			WriteJSONError(w, http.StatusForbidden, "loopback_required", "会话只接受本机主机", nil)
			return
		}
		if !safeMethod(r.Method) {
			if err := CheckSameOrigin(r); err != nil {
				code := "origin_mismatch"
				msg := "非安全方法必须带同源 Origin"
				if errors.Is(err, ErrOriginMismatch) {
					msg = "Origin 与 Host 不同源"
				}
				WriteJSONError(w, http.StatusForbidden, code, msg, nil)
				return
			}
			if err := a.Sessions.VerifyCSRF(r, sess); err != nil {
				WriteJSONError(w, http.StatusForbidden, "csrf_failed", "CSRF 双提交不一致", nil)
				return
			}
		}
		id := Identity{
			Actor:     "session:" + sess.ID,
			Scopes:    []Scope{ScopeRead, ScopeWrite}, // 浏览器控制面不授 admin；admin 只发令牌
			Via:       "session",
			SessionID: sess.ID,
		}
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}

// RequireScope 要求当前身份具备指定作用域；无身份 401，作用域不足 403。
func (a *Authenticator) RequireScope(need Scope, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := IdentityFrom(r.Context())
		if !ok {
			WriteJSONError(w, http.StatusUnauthorized, "unauthenticated", "缺少凭据", nil)
			return
		}
		if !id.HasScope(need) {
			WriteJSONError(w, http.StatusForbidden, "insufficient_scope",
				"凭据作用域不足："+string(need), nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WriteJSONError 输出契约 §6 的固定错误体。
//
// 绝不把请求里的凭据、URL 或 body 拼进 message：错误体是回显泄漏最常见的入口。
func WriteJSONError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	body := map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":      code,
			"message":   Redact(message),
			"retryable": false,
		},
	}
	if len(details) > 0 {
		body["error"].(map[string]any)["details"] = details
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// tokenInQuery 检测 URL 里是否夹带令牌。只做形态判断，不解析、不记录。
func tokenInQuery(r *http.Request) bool {
	for _, values := range r.URL.Query() {
		for _, v := range values {
			if strings.HasPrefix(v, TokenPrefix) {
				return true
			}
		}
	}
	return false
}

// parseBearer 解析 `Bearer <token>`；scheme 不区分大小写，令牌内不得含空白。
func parseBearer(raw string) (string, bool) {
	const scheme = "bearer "
	if len(raw) <= len(scheme) || !strings.EqualFold(raw[:len(scheme)], scheme) {
		return "", false
	}
	token := strings.TrimSpace(raw[len(scheme):])
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", false
	}
	return token, true
}
