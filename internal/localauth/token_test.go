package localauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *TokenStore {
	t.Helper()
	s, err := OpenTokenStore(filepath.Join(t.TempDir(), "auth", "tokens.json"))
	if err != nil {
		t.Fatalf("打开令牌库失败：%v", err)
	}
	return s
}

func TestTokenFileIsRestricted(t *testing.T) {
	s := newStore(t)
	plain, _, err := s.Mint("agent-1", ScopeRead)
	if err != nil {
		t.Fatalf("签发失败：%v", err)
	}
	if !strings.HasPrefix(plain, TokenPrefix) {
		t.Fatalf("令牌必须带 %q 前缀：%s", TokenPrefix, plain)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("读取令牌文件失败：%v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("令牌文件权限必须是 0600，实际 %04o", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(s.Path()))
	if err != nil {
		t.Fatalf("读取令牌目录失败：%v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("令牌目录不得对组/其他可读：%04o", perm)
	}
	// 文件里不得出现明文令牌。
	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if strings.Contains(string(raw), plain) {
		t.Fatal("令牌文件不得保存明文")
	}
}

func TestOpenRejectsLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	s, err := OpenTokenStore(path)
	if err != nil {
		t.Fatalf("初始化失败：%v", err)
	}
	_ = s
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod 失败：%v", err)
	}
	if _, err := OpenTokenStore(path); err == nil {
		t.Fatal("文件权限 0644 时必须拒绝启动，而不是先跑起来")
	}

	loose := filepath.Join(dir, "loose", "tokens.json")
	if err := os.MkdirAll(filepath.Dir(loose), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.Chmod(filepath.Dir(loose), 0o777); err != nil {
		t.Fatalf("chmod 失败：%v", err)
	}
	if _, err := OpenTokenStore(loose); err == nil {
		t.Fatal("组/其他人可写的目录必须拒绝")
	}
}

func TestMintLookupAndRevoke(t *testing.T) {
	s := newStore(t)
	plain, rec, err := s.Mint("agent-1", ScopeRead, ScopeWrite)
	if err != nil {
		t.Fatalf("签发失败：%v", err)
	}
	if rec.ID == "" || !rec.HasScope(ScopeRead) || !rec.HasScope(ScopeWrite) {
		t.Fatalf("记录不完整：%+v", rec)
	}
	if rec.HasScope(ScopeAdmin) {
		t.Fatal("read+write 不等于 admin")
	}
	got, err := s.Lookup(plain)
	if err != nil || got.Actor != "agent-1" {
		t.Fatalf("查令牌失败：%+v %v", got, err)
	}
	if err := s.Revoke(got.ID); err != nil {
		t.Fatalf("撤销失败：%v", err)
	}
	if _, err := s.Lookup(plain); !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("撤销后必须立即失效，得到 %v", err)
	}
	list := s.List()
	if len(list) != 1 || list[0].RevokedAt == nil {
		t.Fatalf("撤销必须反映在记录里：%+v", list)
	}
}

func TestLookupRejectsBadTokens(t *testing.T) {
	s := newStore(t)
	if _, err := s.Lookup("not-a-token"); !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("形态非法应报 ErrTokenMalformed，得到 %v", err)
	}
	// 形态合法但没签发过。
	fake := TokenPrefix + strings.Repeat("A", 43)
	if _, err := s.Lookup(fake); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("未签发应报 ErrTokenInvalid，得到 %v", err)
	}
	if _, _, err := s.Mint("  ", ScopeRead); err == nil {
		t.Fatal("空 actor 不得签发")
	}
	if _, _, err := s.Mint("a", "root"); err == nil {
		t.Fatal("未知作用域不得签发")
	}
}

func TestScopeCovers(t *testing.T) {
	cases := []struct {
		have Scope
		need Scope
		ok   bool
	}{
		{ScopeAdmin, ScopeRead, true},
		{ScopeAdmin, ScopeWrite, true},
		{ScopeWrite, ScopeRead, true},
		{ScopeWrite, ScopeAdmin, false},
		{ScopeRead, ScopeWrite, false},
		{ScopeRead, "root", false},
	}
	for _, c := range cases {
		if got := c.have.Allows(c.need); got != c.ok {
			t.Errorf("%s 覆盖 %s = %v，期望 %v", c.have, c.need, got, c.ok)
		}
	}
}

func TestRedact(t *testing.T) {
	mixed := "Authorization: Bearer maw_pat_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	got := Redact(mixed)
	if strings.Contains(got, "abcdefghijklmnop") {
		t.Fatalf("令牌明文必须被脱敏：%s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("脱敏后应留占位符：%s", got)
	}
}
