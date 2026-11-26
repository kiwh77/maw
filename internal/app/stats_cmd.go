package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/identity"
)

// StatsReportInput 是外部 Agent／受管节点主动写入的一次统计观测。
//
// 它补齐「提示词领取 + 外部 Agent 执行」这一路径：没有 runner 去读 CLI 会话，
// 就由执行者自己把轮次与 token 交回来，看板才有轮次用量可算。
type StatsReportInput struct {
	ProjectID string
	Actor     string
	TaskID    string
	Client    string
	Model     string
	SessionID string
	SourceNS  string

	Turns            int
	InputTokens      *int64
	OutputTokens     *int64
	CacheReadTokens  *int64
	CacheWriteTokens *int64
	DurationSeconds  int
	StartedAt        string
	Quality          string
}

// StatsReport 把一次执行观测写进统计台账：来源会话、轮次、Run 关联与用量。
//
// 缺什么就不写什么：没有 token 就不写用量记录（0 不能冒充未知）；
// 但轮次仍然记账，至少让「执行过多少轮」可见。
func StatsReport(cwd string, in StatsReportInput) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, i18n.E("err.resolve_project", "next.init_or_project")
	}

	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		projectID = pc.ID()
	}

	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor, err = identity.Resolve(cwd, "")
		if err != nil {
			return nil, err
		}
	}

	taskID := strings.TrimSpace(in.TaskID)
	var task *db.Task
	if taskID != "" {
		err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
			var e error
			task, e = db.GetTask(tx, projectID, taskID)
			return e
		})
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, i18n.E("err.task_missing", "next.task_missing", taskID)
		}
	} else {
		task, err = activeTaskOf(pc, actor)
		if err != nil {
			return nil, err
		}
		if task == nil {
			return nil, i18n.E("err.stats_task", "next.stats_task")
		}
		taskID = task.ID
	}
	if task.Owner != actor {
		return nil, i18n.E("err.stats_owner", "next.stats_owner", taskID, actor)
	}

	turns := in.Turns
	if turns <= 0 {
		turns = 1
	}
	if in.DurationSeconds < 0 {
		return nil, i18n.E("err.stats_duration", "next.stats_duration")
	}
	if negativeToken(in.InputTokens) || negativeToken(in.OutputTokens) ||
		negativeToken(in.CacheReadTokens) || negativeToken(in.CacheWriteTokens) {
		return nil, i18n.E("err.stats_tokens", "next.stats_tokens")
	}

	quality := strings.TrimSpace(in.Quality)
	if quality == "" {
		if hasUsageInput(in) {
			quality = string(model.QualityObserved)
		} else {
			quality = string(model.QualityUnknown)
		}
	}
	q := model.Quality(quality)
	if !q.Valid() {
		return nil, i18n.E("err.stats_quality", "next.stats_quality", quality)
	}
	if q == model.QualityUnknown && hasUsageInput(in) {
		return nil, i18n.E("err.stats_quality_tokens", "next.stats_quality_tokens")
	}

	started := time.Now().UTC()
	if strings.TrimSpace(in.StartedAt) != "" {
		t, e := time.Parse(time.RFC3339, strings.TrimSpace(in.StartedAt))
		if e != nil {
			return nil, i18n.EW("err.stats_started_at", "next.stats_started_at", e)
		}
		started = t.UTC()
	}
	ended := started.Add(time.Duration(in.DurationSeconds) * time.Second)

	sourceNS := normalizeSourceNS(in.SourceNS, in.Client)
	externalSession := strings.TrimSpace(in.SessionID)
	if externalSession == "" {
		externalSession = actor + ":" + taskID
	}
	sessionID := sourceNS + ":" + externalSession

	reportID := statsReportID()
	parts := splitTokenValues(in.InputTokens, in.OutputTokens, in.CacheReadTokens, in.CacheWriteTokens, turns)

	meta := map[string]any{"agent": actor, "task_id": taskID, "client": in.Client, "model": in.Model, "report": reportID}
	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	var runID, instanceID string
	err = store.Write(func(tx *sql.Tx) error {
		p, e := db.GetProject(tx, projectID)
		if e != nil {
			return e
		}
		if p == nil {
			return i18n.E("err.project_missing", "next.run_init", projectID)
		}
		if r, e := db.ActiveRunForInstance(tx, task.InstanceID); e != nil {
			return e
		} else if r != nil {
			runID = r.ID
			instanceID = r.InstanceID
		}
		if instanceID == "" {
			instanceID = task.InstanceID
		}

		if e := db.UpsertSourceSession(tx, db.SourceSession{
			SessionID:  sessionID,
			ProjectID:  projectID,
			SourceNS:   sourceNS,
			ExternalID: externalSession,
			Adapter:    sourceNS,
			OpenedAt:   started.Format(time.RFC3339Nano),
			ClosedAt:   ended.Format(time.RFC3339Nano),
			Meta:       meta,
		}); e != nil {
			return e
		}

		for i := 0; i < turns; i++ {
			turnID := fmt.Sprintf("%s:%s:%s-%d", sourceNS, externalSession, reportID, i)
			if e := db.UpsertSourceTurn(tx, db.SourceTurn{
				TurnID:     turnID,
				SourceNS:   sourceNS,
				SessionID:  sessionID,
				ExternalID: reportID,
				StartedAt:  started.Format(time.RFC3339Nano),
				EndedAt:    ended.Format(time.RFC3339Nano),
				Meta: map[string]any{
					"record_type": "result",
					"record_ref":  "stats:" + reportID,
					"client":      in.Client,
					"model":       in.Model,
				},
			}); e != nil {
				return e
			}
			if e := db.EnsureRunSourceLink(tx, db.RunSourceLink{
				LinkID:     statsLinkID(runID, turnID),
				ProjectID:  projectID,
				RunID:      runID,
				InstanceID: instanceID,
				SessionID:  sessionID,
				TurnID:     turnID,
				Attributed: true,
			}); e != nil {
				return e
			}
			item := parts[i]
			if item.input == nil && item.output == nil && item.cacheRead == nil && item.cacheWrite == nil {
				continue
			}
			if e := db.PutUsageRecord(tx, db.UsageRecord{
				UsageID:          "usage:" + turnID + ":1:run",
				SourceNS:         sourceNS,
				TurnID:           turnID,
				Epoch:            1,
				Scope:            "run",
				Semantic:         db.UsageDelta,
				InputTokens:      item.input,
				OutputTokens:     item.output,
				CacheReadTokens:  item.cacheRead,
				CacheWriteTokens: item.cacheWrite,
				TotalTokens:      item.total,
				Quality:          q,
				Basis:            "external_agent_report",
			}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := Result{
		"ok":               true,
		"project_id":       projectID,
		"task_id":          taskID,
		"agent":            actor,
		"source_ns":        sourceNS,
		"session_id":       sessionID,
		"turns":            turns,
		"duration_seconds": in.DurationSeconds,
		"quality":          quality,
		"next":             i18n.T("next.stats_reported"),
	}
	if in.InputTokens != nil {
		out["input_tokens"] = *in.InputTokens
	}
	if in.OutputTokens != nil {
		out["output_tokens"] = *in.OutputTokens
	}
	return out, nil
}

type statsTokenPart struct {
	input, output, cacheRead, cacheWrite, total *int64
}

func hasUsageInput(in StatsReportInput) bool {
	return in.InputTokens != nil || in.OutputTokens != nil ||
		in.CacheReadTokens != nil || in.CacheWriteTokens != nil
}

func negativeToken(v *int64) bool {
	return v != nil && *v < 0
}

// splitTokenValues 把一次上报的汇总 token 摊到各轮：轮次是事实，token 也是事实，
// 汇总值只有摊平后才能同时进入「轮次数」与「token 合计」两个统计口径。
func splitTokenValues(input, output, cacheRead, cacheWrite *int64, turns int) []statsTokenPart {
	out := make([]statsTokenPart, turns)
	for i := range out {
		out[i] = statsTokenPart{
			input:      shareInt64(input, turns, i),
			output:     shareInt64(output, turns, i),
			cacheRead:  shareInt64(cacheRead, turns, i),
			cacheWrite: shareInt64(cacheWrite, turns, i),
		}
		in, ou := out[i].input, out[i].output
		if in != nil && ou != nil {
			v := *in + *ou
			out[i].total = &v
		}
	}
	return out
}

func shareInt64(total *int64, n, i int) *int64 {
	if total == nil {
		return nil
	}
	base := *total / int64(n)
	rem := *total % int64(n)
	v := base
	if i == 0 {
		v += rem
	}
	return &v
}

func normalizeSourceNS(source, client string) string {
	s := strings.TrimSpace(source)
	if s == "" {
		s = strings.TrimSpace(client)
	}
	if s == "" {
		return "external"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "external"
	}
	return out
}

func statsLinkID(runID, turnID string) string {
	if runID != "" {
		return "link:" + runID + ":" + turnID
	}
	return "link:stats:" + turnID
}

func statsReportID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
