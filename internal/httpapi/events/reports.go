package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	srcEvents "github.com/kiwh77/maw/internal/events"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
)

// KindAgentProgress 是旁路进度观测的标准 kind（非控制类）。
const KindAgentProgress = "agent.progress"

// StoreReporter 用 events.Ingestor + db.AppendRunEvent 落观测事实。
//
// 进程内按幂等键去重；同键同摘要重放，同键异摘要冲突。
// 绝不调用任务状态迁移。
type StoreReporter struct {
	Store *db.Store
	Now   func() time.Time

	mu    sync.Mutex
	ing   *srcEvents.Ingestor
	idem  map[string]idemEntry
}

type idemEntry struct {
	digest string
	result ReportResult
}

// NewStoreReporter 构造落库上报器。
func NewStoreReporter(store *db.Store) *StoreReporter {
	now := time.Now
	return &StoreReporter{
		Store: store,
		Now:   now,
		ing:   srcEvents.New(srcEvents.Options{Now: now}),
		idem:  map[string]idemEntry{},
	}
}

// Accept 实现 Reporter。
func (s *StoreReporter) Accept(ctx context.Context, req ReportRequest) (ReportResult, error) {
	_ = ctx
	if s.Now == nil {
		s.Now = time.Now
	}
	digest := reportDigest(req)
	idemKey := req.IdempotencyKey + "|" + req.Actor + "|POST|/api/v1/status-reports"
	s.mu.Lock()
	if prev, ok := s.idem[idemKey]; ok {
		if prev.digest != digest {
			s.mu.Unlock()
			return ReportResult{}, errConflict{
				Code: "idempotency_key_reuse",
				Msg:  "同键不同请求内容",
			}
		}
		out := prev.result
		out.IdempotentReplay = true
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	src := req.Source
	if src.Domain == "" {
		src.Domain = srcEvents.DomainObserved
	}
	if src.Adapter == "" {
		src.Adapter = "status-report"
	}
	if src.Agent == "" && req.Actor != "" {
		src.Agent = model.AgentID(req.Actor)
	}

	batch := append([]srcEvents.Observation(nil), req.Observations...)
	if len(batch) == 0 && (req.LegacyMessage != "" || req.LegacyStatus != "" || req.LegacyPhase != "") {
		now := s.Now().UTC()
		batch = append(batch, srcEvents.Observation{
			RecordRef:     "status-report:" + req.IdempotencyKey,
			RecordType:    "agent.status",
			SchemaVersion: 1,
			SourceKey:     req.IdempotencyKey,
			Kind:          KindAgentProgress,
			OccurredAt:    &now,
			Quality:       model.QualityObserved,
			Payload: map[string]any{
				"message":      req.LegacyMessage,
				"phase":        req.LegacyPhase,
				"pct":          req.LegacyPct,
				"agent_status": req.LegacyStatus,
				"task_ref":     req.LegacyTaskRef,
				"project_id":   req.ProjectID,
			},
		})
	}
	if len(batch) == 0 {
		return ReportResult{}, errInvalid{Code: "invalid_body", Msg: "需要 observations 或进度字段"}
	}

	res := s.ing.Ingest(src, batch)
	out := ReportResult{
		Accepted:          len(res.Accepted),
		Duplicates:        len(res.Duplicates),
		Conflicts:         len(res.Conflicts),
		Rejected:          len(res.Rejected),
		ChangedTaskStatus: false,
	}
	for _, rej := range res.Rejected {
		out.RejectionSummaries = append(out.RejectionSummaries, rej.Error())
	}

	obsByRef := map[string]srcEvents.Observation{}
	for _, o := range batch {
		obsByRef[o.RecordRef] = o
	}

	if s.Store != nil && len(res.Accepted) > 0 {
		err := s.Store.Write(func(tx *sql.Tx) error {
			for _, rec := range res.Accepted {
				ev := rec.Event
				payload := map[string]any{
					"source_key":   rec.Key,
					"fingerprint":  rec.Fingerprint,
					"record_ref":   rec.RecordRef,
					"record_type":  rec.RecordType,
					"source":       src,
					"attribution":  rec.Attribution,
					"reported":     rec.Reported,
					"late_arrival": rec.LateArrival,
					"backfill":     rec.Backfill,
				}
				if o, ok := obsByRef[rec.RecordRef]; ok && o.Payload != nil {
					for k, v := range o.Payload {
						payload[k] = v
					}
				}
				if len(rec.QualityNotes) > 0 {
					payload["quality_notes"] = rec.QualityNotes
				}
				inst := string(rec.Reported.Instance)
				if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
					ID:            ev.EventID,
					ProjectID:     req.ProjectID,
					RunID:         string(ev.RunID),
					InstanceID:    inst,
					Kind:          ev.Kind,
					SchemaVersion: ev.SchemaVersion,
					Quality:       ev.Quality,
					OccurredAt:    ev.OccurredAt,
					ReceivedAt:    ev.ReceivedAt,
					Payload:       payload,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return ReportResult{}, err
		}
	}

	s.mu.Lock()
	s.idem[idemKey] = idemEntry{digest: digest, result: out}
	s.mu.Unlock()
	return out, nil
}

func reportDigest(req ReportRequest) string {
	b, _ := json.Marshal(struct {
		ProjectID    string
		Source       srcEvents.Source
		Observations []srcEvents.Observation
		LegacyMessage, LegacyPhase, LegacyStatus, LegacyTaskRef string
		LegacyPct                                               int
	}{
		ProjectID: req.ProjectID, Source: req.Source, Observations: req.Observations,
		LegacyMessage: req.LegacyMessage, LegacyPhase: req.LegacyPhase,
		LegacyStatus: req.LegacyStatus, LegacyTaskRef: req.LegacyTaskRef, LegacyPct: req.LegacyPct,
	})
	return string(b)
}

type errConflict struct{ Code, Msg string }
type errInvalid struct{ Code, Msg string }

func (e errConflict) Error() string { return e.Code + ": " + e.Msg }
func (e errInvalid) Error() string  { return e.Code + ": " + e.Msg }

func (a *API) handleStatusReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return
	}
	if a.Reports == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "Reports 未注入", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_body", "无法读取请求体", nil)
		return
	}
	var raw struct {
		Actor        string                   `json:"actor"`
		ProjectID    string                   `json:"project_id"`
		TaskID       string                   `json:"task_id"`
		Status       string                   `json:"status"`
		Message      string                   `json:"message"`
		Phase        string                   `json:"phase"`
		Pct          int                      `json:"pct"`
		Source       srcEvents.Source         `json:"source"`
		Observations []srcEvents.Observation  `json:"observations"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &raw); err != nil {
			writeErr(w, http.StatusUnprocessableEntity, "invalid_body", "JSON 无法解析", nil)
			return
		}
	}

	actor := strings.TrimSpace(raw.Actor)
	if id, ok := localauth.IdentityFrom(r.Context()); ok && actor == "" {
		actor = id.Actor
	}
	if actor == "" {
		writeErr(w, http.StatusUnprocessableEntity, "actor_required", "必须提供 actor", map[string]any{"field": "actor"})
		return
	}
	// 有身份时 actor 必须与令牌一致（H171）。
	if id, ok := localauth.IdentityFrom(r.Context()); ok && id.Actor != "" && id.Actor != actor {
		writeErr(w, http.StatusForbidden, "actor_mismatch", "actor 必须与令牌身份一致", nil)
		return
	}

	out, err := a.Reports.Accept(r.Context(), ReportRequest{
		Actor: actor, ProjectID: strings.TrimSpace(raw.ProjectID), IdempotencyKey: key,
		Source: raw.Source, Observations: raw.Observations,
		LegacyMessage: raw.Message, LegacyPhase: raw.Phase, LegacyPct: raw.Pct,
		LegacyTaskRef: raw.TaskID, LegacyStatus: raw.Status,
	})
	if err != nil {
		var c errConflict
		var inv errInvalid
		switch {
		case asConflict(err, &c):
			writeErr(w, http.StatusConflict, c.Code, c.Msg, nil)
		case asInvalid(err, &inv):
			writeErr(w, http.StatusUnprocessableEntity, inv.Code, inv.Msg, nil)
		default:
			writeErr(w, http.StatusInternalServerError, "internal_error", "上报失败", nil)
		}
		return
	}

	writeOK(w, http.StatusOK, map[string]any{
		"changed_task_status": out.ChangedTaskStatus,
		"accepted":            out.Accepted,
		"duplicates":          out.Duplicates,
		"conflicts":           out.Conflicts,
		"rejected":            out.Rejected,
		"idempotent_replay":   out.IdempotentReplay,
		"rejections":          out.RejectionSummaries,
	})
}

func asConflict(err error, dst *errConflict) bool {
	if e, ok := err.(errConflict); ok {
		*dst = e
		return true
	}
	return false
}

func asInvalid(err error, dst *errInvalid) bool {
	if e, ok := err.(errInvalid); ok {
		*dst = e
		return true
	}
	return false
}
