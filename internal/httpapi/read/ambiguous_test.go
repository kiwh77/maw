package read

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAmbiguousErrorMapsTo409 钉住契约：显示编号歧义不得静默取第一条。
func TestAmbiguousErrorMapsTo409(t *testing.T) {
	rec := httptest.NewRecorder()
	ae := ambiguousDisplay("VN-001", 2).(ambiguousError)
	writeErr(rec, http.StatusConflict, "ambiguous_display_id",
		"显示编号命中多个活跃实例，请改用 task_instance",
		map[string]any{"display_id": ae.DisplayID, "count": ae.Count})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ambiguous_display_id") || !strings.Contains(body, "VN-001") {
		t.Fatalf("body=%s", body)
	}
}
