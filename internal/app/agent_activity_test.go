package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
)

func agentForActivity(id, status, taskID string, lastSeen time.Time) *db.Agent {
	return &db.Agent{
		ID: id, Status: status, TaskID: taskID,
		LastSeen: lastSeen.UTC().Format(time.RFC3339),
	}
}

// working：有任务、状态 working、心跳在窗口内。
func TestAgentActivityWorking(t *testing.T) {
	now := time.Now().UTC()
	a := agentForActivity("a-working", "working", "T-1", now.Add(-time.Minute))
	if got := board.DeriveAgentActivity(a, now); got != board.AgentActivityWorking {
		t.Fatalf("activity=%q，期望 working", got)
	}
	snap := board.BuildSnapshot(nil, []*db.Agent{a}, 1)
	if got := AgentActivityFor(snap, "a-working"); got != "working" {
		t.Fatalf("快照 activity=%q，期望 working", got)
	}
}

// stalled：占着任务但心跳过期；offline 却仍占任务同样算停滞。
func TestAgentActivityStalled(t *testing.T) {
	now := time.Now().UTC()
	stale := agentForActivity("a-stale", "working", "T-2", now.Add(-time.Hour))
	if got := board.DeriveAgentActivity(stale, now); got != board.AgentActivityStalled {
		t.Fatalf("过期心跳 activity=%q，期望 stalled", got)
	}
	offline := agentForActivity("a-offline", "offline", "T-3", now.Add(-time.Minute))
	if got := board.DeriveAgentActivity(offline, now); got != board.AgentActivityStalled {
		t.Fatalf("offline 占任务 activity=%q，期望 stalled", got)
	}
	// 时间不可解析 = 证据不足，按停滞处理，不假装还在推进。
	unknown := &db.Agent{ID: "a-unknown", Status: "working", TaskID: "T-4", LastSeen: "not-a-time"}
	if got := board.DeriveAgentActivity(unknown, now); got != board.AgentActivityStalled {
		t.Fatalf("时间不可解析 activity=%q，期望 stalled", got)
	}
}

// idle：待命、无任务、nil agent 都不算在推进。
func TestAgentActivityIdle(t *testing.T) {
	now := time.Now().UTC()
	cases := []*db.Agent{
		agentForActivity("a-idle", "idle", "", now.Add(-time.Minute)),
		agentForActivity("a-notask", "working", "", now.Add(-time.Minute)),
		agentForActivity("a-offline-idle", "offline", "", now.Add(-time.Hour)),
		nil,
	}
	for _, a := range cases {
		if got := board.DeriveAgentActivity(a, now); got != board.AgentActivityIdle {
			t.Fatalf("agent=%+v activity=%q，期望 idle", a, got)
		}
	}
	if got := AgentActivityFor(board.Snapshot{}, "missing"); got != "idle" {
		t.Fatalf("未知 agent 应保守为 idle，实际 %q", got)
	}
}

// 快照 JSON 必须带 activity 字段，三态计数与推导一致。
func TestAgentActivitySnapshotAndCounts(t *testing.T) {
	now := time.Now().UTC()
	agents := []*db.Agent{
		agentForActivity("a-working", "working", "T-1", now.Add(-time.Minute)),
		agentForActivity("a-stalled", "working", "T-2", now.Add(-time.Hour)),
		agentForActivity("a-idle", "idle", "", now.Add(-time.Minute)),
	}
	snap := board.BuildSnapshot(nil, agents, 1)
	if snap.AgentActivity == nil {
		t.Fatal("快照缺少 agent_activity")
	}
	counts := AgentActivityCounts(snap)
	if counts["working"] != 1 || counts["stalled"] != 1 || counts["idle"] != 1 {
		t.Fatalf("三态计数错误: %+v / map=%+v", counts, snap.AgentActivity)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"agent_activity"`) ||
		!strings.Contains(string(raw), `"stalled"`) {
		t.Fatalf("快照 JSON 未暴露 activity: %s", raw[:min(len(raw), 400)])
	}
}

// CQ-003 不回退：activity 只是展示推导，不改 Agent 权威状态、不释放占用。
func TestAgentActivityExpireSemanticsUntouched(t *testing.T) {
	a := agentForActivity("a1", "working", "T-9", time.Now().UTC().Add(-time.Hour))
	status, task := a.Status, a.TaskID
	if got := board.DeriveAgentActivity(a, time.Now().UTC()); got != board.AgentActivityStalled {
		t.Fatalf("activity=%q，期望 stalled", got)
	}
	if a.Status != status || a.TaskID != task {
		t.Fatal("推导不得反向修改 Agent 状态或任务占用")
	}
}
