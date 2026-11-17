package db

import (
	"database/sql"
	"sort"
	"strings"
)

// Agent 的「标签 / 能力角色」存放在 agents.meta_json 里，不新增列：
// 这两个键是**展示与选择**用的说明性信息，不参与状态机，也不改变 Role()
// 的三类语义（worker / integrator / reviewer）。
//
// 为什么放 meta：
//   - 标签是可选、可增删的，加列就得迁移数据库；
//   - 看板与 `maw agents --json` 已经把 meta 原样透出，存进去即可在列表里看到；
//   - 契约（maw.collaboration.primitives/v1 §5）只要求「本机可标记」，不要求索引。
const (
	AgentMetaTags      = "tags"
	AgentMetaRoles     = "roles"
	AgentMetaBoundLane = "bound_lane"
)

// AgentBoundLane 读回该 Agent 粘住的子队列；未绑定返回空串。
func AgentBoundLane(tx *sql.Tx, projectID, agentID string) (string, error) {
	a, err := GetAgent(tx, projectID, agentID)
	if err != nil {
		return "", err
	}
	if a == nil {
		return "", nil
	}
	return metaString(a.Meta, AgentMetaBoundLane), nil
}

// SetAgentBoundLane 写入或清除子队列绑定；lane 空串表示解除绑定。
func SetAgentBoundLane(tx *sql.Tx, projectID, agentID, lane string) error {
	a, err := GetAgent(tx, projectID, agentID)
	if err != nil {
		return err
	}
	if a == nil {
		return &AgentNotFound{ProjectID: projectID, AgentID: agentID}
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	lane = strings.TrimSpace(lane)
	if lane == "" {
		delete(a.Meta, AgentMetaBoundLane)
	} else {
		a.Meta[AgentMetaBoundLane] = lane
	}
	return UpsertAgent(tx, a)
}

func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return ""
	}
	s, _ := raw.(string)
	return strings.TrimSpace(s)
}

// SetAgentLabels 覆盖式写入某 Agent 的 tags / roles。
//
// 传 nil 表示「不改这一项」，传空切片表示「清空」。这样调用方可以只改 tags
// 而不小心抹掉 roles。
func SetAgentLabels(tx *sql.Tx, projectID, agentID string, tags, roles []string) error {
	a, err := GetAgent(tx, projectID, agentID)
	if err != nil {
		return err
	}
	if a == nil {
		return &AgentNotFound{ProjectID: projectID, AgentID: agentID}
	}
	if a.Meta == nil {
		a.Meta = map[string]any{}
	}
	if tags != nil {
		a.Meta[AgentMetaTags] = normalizeLabels(tags)
	}
	if roles != nil {
		a.Meta[AgentMetaRoles] = normalizeLabels(roles)
	}
	return UpsertAgent(tx, a)
}

// AgentLabels 读回 tags / roles；缺省返回空切片而不是 nil，便于直接比较。
func AgentLabels(tx *sql.Tx, projectID, agentID string) (tags, roles []string, err error) {
	a, err := GetAgent(tx, projectID, agentID)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, &AgentNotFound{ProjectID: projectID, AgentID: agentID}
	}
	return metaLabels(a.Meta, AgentMetaTags), metaLabels(a.Meta, AgentMetaRoles), nil
}

// AgentHasLabel 判断标签是否命中 tags 或 roles（两者在派发选择上等价）。
func AgentHasLabel(tx *sql.Tx, projectID, agentID, label string) (bool, error) {
	tags, roles, err := AgentLabels(tx, projectID, agentID)
	if err != nil {
		return false, err
	}
	want := strings.TrimSpace(label)
	for _, l := range append(append([]string{}, tags...), roles...) {
		if l == want {
			return true, nil
		}
	}
	return false, nil
}

// AgentNotFound 是「目标 Agent 未注册」的稳定错误形状。
type AgentNotFound struct {
	ProjectID string
	AgentID   string
}

func (e *AgentNotFound) Error() string {
	return "agent_not_found: " + e.AgentID + " 未在本工程注册"
}

func (e *AgentNotFound) Code() string { return "agent_not_found" }

func normalizeLabels(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

func metaLabels(meta map[string]any, key string) []string {
	if meta == nil {
		return []string{}
	}
	raw, ok := meta[key]
	if !ok || raw == nil {
		return []string{}
	}
	switch v := raw.(type) {
	case []string:
		return normalizeLabels(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeLabels(out)
	case string:
		if v == "" {
			return []string{}
		}
		return normalizeLabels([]string{v})
	}
	return []string{}
}
