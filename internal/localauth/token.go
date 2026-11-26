package localauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// TokenPrefix 是 CLI 令牌的固定前缀；与契约 §7.2 的 `maw_pat_<...>` 对齐。
const TokenPrefix = "maw_pat_"

var (
	// ErrTokenMalformed 形态不合法（不是本系统签发的形状）。
	ErrTokenMalformed = errors.New("localauth: 令牌形态非法")
	// ErrTokenInvalid 查不到对应记录（伪造、已删除或换了数据目录）。
	ErrTokenInvalid = errors.New("localauth: 令牌无效")
	// ErrTokenRevoked 已撤销；撤销即时生效。
	ErrTokenRevoked = errors.New("localauth: 令牌已撤销")
)

// Scope 是令牌作用域，三层包含：admin ⊇ write ⊇ read。
type Scope string

// 三个作用域。
const (
	ScopeRead  Scope = "read"
	ScopeWrite Scope = "write"
	ScopeAdmin Scope = "admin"
)

// Valid 报告作用域是否合法。
func (s Scope) Valid() bool { return s == ScopeRead || s == ScopeWrite || s == ScopeAdmin }

// Allows 报告该令牌作用域能否满足所需作用域。
func (s Scope) Allows(need Scope) bool {
	if !need.Valid() {
		return false
	}
	switch s {
	case ScopeAdmin:
		return true
	case ScopeWrite:
		return need == ScopeRead || need == ScopeWrite
	case ScopeRead:
		return need == ScopeRead
	}
	return false
}

// Token 是一条令牌记录（不含明文）。明文只在签发那一刻出现一次。
type Token struct {
	ID        string     `json:"id"`
	Actor     string     `json:"actor"`
	Scopes    []Scope    `json:"scopes"`
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

// HasScope 报告该令牌是否满足所需作用域。
func (t Token) HasScope(need Scope) bool {
	for _, s := range t.Scopes {
		if s.Allows(need) {
			return true
		}
	}
	return false
}

// Revoked 报告是否已撤销。
func (t Token) Revoked() bool { return t.RevokedAt != nil }

type tokenRecord struct {
	ID        string     `json:"id"`
	Actor     string     `json:"actor"`
	Scopes    []Scope    `json:"scopes"`
	Hash      string     `json:"hash"` // base64(sha256(明文))，文件里不存明文
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
}

type tokenFile struct {
	Tokens []tokenRecord `json:"tokens"`
}

// TokenStore 是文件支撑的令牌库。
//
// 文件权限必须是 0600、目录 0700：同用户进程仍可读（契约 §7.5 明确不承诺强隔离），
// 但不能被同机其他用户读到。加载时若发现权限过宽直接报错，而不是「先跑起来再说」。
type TokenStore struct {
	path string
	mu   sync.Mutex
	recs []tokenRecord
	now  func() time.Time
}

var tokenRE = regexp.MustCompile(`^maw_pat_[A-Za-z0-9_-]{43}$`)

// OpenTokenStore 打开（或初始化）令牌库。目录与文件都会按受限权限创建。
func OpenTokenStore(path string) (*TokenStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("localauth: 令牌文件路径不得为空")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("localauth: 创建令牌目录失败: %w", err)
	}
	// 目录只要求「不能被组/其他人写入」：已有的 ~/.config、~/.maw 常见是 0755，
	// 强制 0700 会让正常安装直接失败；真正的凭据保护靠文件本身的 0600。
	if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("localauth: 令牌目录可被组/其他写入 %04o，拒绝使用", info.Mode().Perm())
	}

	s := &TokenStore{path: path, now: time.Now}
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// 新建空库：先落一个 0600 文件，避免「先写宽权限再收紧」的窗口。
		if err := s.persist(); err != nil {
			return nil, err
		}
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("localauth: 读取令牌文件失败: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("localauth: 令牌文件权限过宽 %04o，需要 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("localauth: 读取令牌文件失败: %w", err)
	}
	var f tokenFile
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &f); err != nil {
			return nil, fmt.Errorf("localauth: 令牌文件损坏: %w", err)
		}
	}
	s.recs = f.Tokens
	return s, nil
}

// Path 返回令牌文件路径；仅供本机诊断使用，**不得**写进 HTTP 响应。
func (s *TokenStore) Path() string { return s.path }

// Mint 签发一枚新令牌，返回明文（只此一次）与记录。
func (s *TokenStore) Mint(actor string, scopes ...Scope) (string, Token, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", Token{}, errors.New("localauth: 令牌必须绑定 actor")
	}
	if len(scopes) == 0 {
		return "", Token{}, errors.New("localauth: 至少给一个作用域")
	}
	for _, sc := range scopes {
		if !sc.Valid() {
			return "", Token{}, fmt.Errorf("localauth: 未知作用域 %q", sc)
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", Token{}, fmt.Errorf("localauth: 生成令牌失败: %w", err)
	}
	plain := TokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	id := "pat_" + base64.RawURLEncoding.EncodeToString(buf[:8])
	hash := hashToken(plain)

	s.mu.Lock()
	defer s.mu.Unlock()
	rec := tokenRecord{
		ID: id, Actor: actor, Scopes: append([]Scope(nil), scopes...),
		Hash: hash, CreatedAt: s.now().UTC(),
	}
	s.recs = append(s.recs, rec)
	if err := s.persist(); err != nil {
		s.recs = s.recs[:len(s.recs)-1]
		return "", Token{}, err
	}
	return plain, Token{ID: id, Actor: actor, Scopes: rec.Scopes, CreatedAt: rec.CreatedAt}, nil
}

// Lookup 校验明文令牌。撤销是即时的：撤销后同一明文立即失效。
func (s *TokenStore) Lookup(plain string) (Token, error) {
	if !tokenRE.MatchString(plain) {
		return Token{}, ErrTokenMalformed
	}
	sum := hashToken(plain)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.recs {
		// 定长比较，避免用「先命中先返回」泄露前缀信息。
		if subtle.ConstantTimeCompare([]byte(rec.Hash), []byte(sum)) != 1 {
			continue
		}
		if rec.RevokedAt != nil {
			return Token{}, ErrTokenRevoked
		}
		return Token{ID: rec.ID, Actor: rec.Actor, Scopes: rec.Scopes, CreatedAt: rec.CreatedAt}, nil
	}
	return Token{}, ErrTokenInvalid
}

// Revoke 撤销一枚令牌；重复撤销是幂等的。
func (s *TokenStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.recs {
		if s.recs[i].ID != id {
			continue
		}
		if s.recs[i].RevokedAt == nil {
			now := s.now().UTC()
			s.recs[i].RevokedAt = &now
			if err := s.persist(); err != nil {
				s.recs[i].RevokedAt = nil
				return err
			}
		}
		return nil
	}
	return ErrTokenInvalid
}

// List 返回全部令牌记录（不含明文与哈希），用于 `maw auth list` 一类只读视图。
func (s *TokenStore) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Token, 0, len(s.recs))
	for _, rec := range s.recs {
		out = append(out, Token{
			ID: rec.ID, Actor: rec.Actor, Scopes: append([]Scope(nil), rec.Scopes...),
			CreatedAt: rec.CreatedAt, RevokedAt: rec.RevokedAt,
		})
	}
	return out
}

// persist 以「临时文件 + rename」原子落盘，权限固定 0600。
func (s *TokenStore) persist() error {
	f := tokenFile{Tokens: s.recs}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("localauth: 序列化令牌库失败: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tokens-*")
	if err != nil {
		return fmt.Errorf("localauth: 创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("localauth: 设置令牌文件权限失败: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("localauth: 写入令牌文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("localauth: 关闭令牌文件失败: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("localauth: 落盘令牌文件失败: %w", err)
	}
	return nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// tokenLikeRE 匹配日志里可能出现的令牌明文，用于脱敏。
var tokenLikeRE = regexp.MustCompile(`maw_pat_[A-Za-z0-9_-]{8,}`)

// Redact 把文本里的令牌明文替换成固定占位符；写访问日志前必须过一遍。
func Redact(s string) string {
	return tokenLikeRE.ReplaceAllString(s, TokenPrefix+"<redacted>")
}
