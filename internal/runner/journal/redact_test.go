package journal

import (
	"strings"
	"testing"
)

// TestRedactJSONKeepsMeteringTokens 说明计量 token 不能被当凭据抹掉：
// 抹掉之后下游只会看到「没看到」，而不是「看到了多少」。
func TestRedactJSONKeepsMeteringTokens(t *testing.T) {
	payload := []byte(`{"type":"result","session_id":"sess-1","usage":{"input_tokens":120,"output_tokens":34,"cache_read_input_tokens":7}}`)
	clean, n, err := RedactJSON(payload)
	if err != nil {
		t.Fatalf("RedactJSON: %v", err)
	}
	if n != 0 {
		t.Fatalf("替换了 %d 处：计量字段不该被脱敏（%s）", n, clean)
	}
	for _, want := range []string{`"input_tokens":120`, `"output_tokens":34`, `"cache_read_input_tokens":7`} {
		if !strings.Contains(string(clean), want) {
			t.Fatalf("脱敏后丢了 %s：%s", want, clean)
		}
	}
}

// TestRedactJSONStillHidesCredentials 说明凭据照旧脱敏：这次修的是误伤，
// 不是放宽。
func TestRedactJSONStillHidesCredentials(t *testing.T) {
	payload := []byte(`{"api_key":"sk-abcdef123456","access_token":"tok-abcdef123456","session_id":"sess-1"}`)
	clean, n, err := RedactJSON(payload)
	if err != nil {
		t.Fatalf("RedactJSON: %v", err)
	}
	if n < 2 {
		t.Fatalf("只替换了 %d 处：凭据应当被脱敏（%s）", n, clean)
	}
	if strings.Contains(string(clean), "sk-abcdef123456") || strings.Contains(string(clean), "tok-abcdef123456") {
		t.Fatalf("凭据仍在落盘载荷里：%s", clean)
	}
}

// TestRedactJSONKeepsNumericSecretLikeValues 说明数字不是凭据：
// 计量值即使落在敏感字段名下也不该被抹掉。
func TestRedactJSONKeepsNumericSecretLikeValues(t *testing.T) {
	payload := []byte(`{"session_token":12345678,"password":42}`)
	clean, n, err := RedactJSON(payload)
	if err != nil {
		t.Fatalf("RedactJSON: %v", err)
	}
	if n != 0 {
		t.Fatalf("替换了 %d 处：数字不该被当凭据（%s）", n, clean)
	}
	if !strings.Contains(string(clean), `"session_token":12345678`) {
		t.Fatalf("数字计量值被抹掉了：%s", clean)
	}
}
