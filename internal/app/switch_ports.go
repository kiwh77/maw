package app

import (
	"context"

	httpresources "github.com/kiwh77/maw/internal/httpapi/resources"
)

// autoExecutionPort 把工作台「自动执行」开关接到应用用例。
type autoExecutionPort struct{}

func (autoExecutionPort) Get(_ context.Context, projectID string) (httpresources.AutoExecutionState, error) {
	view, err := AutoExecutionGet(projectID)
	if err != nil {
		return httpresources.AutoExecutionState{}, err
	}
	return autoExecutionStateOf(view), nil
}

func (autoExecutionPort) Set(_ context.Context, req httpresources.AutoExecutionSetRequest) (httpresources.AutoExecutionState, error) {
	view, err := AutoExecutionSet(AutoExecutionInput{
		ProjectID: req.ProjectID, Enabled: req.Enabled, NodeID: req.NodeID, Actor: req.Actor,
	})
	if err != nil {
		return httpresources.AutoExecutionState{}, err
	}
	return autoExecutionStateOf(view), nil
}

func autoExecutionStateOf(view AutoExecutionView) httpresources.AutoExecutionState {
	out := httpresources.AutoExecutionState{
		ProjectID:    view.ProjectID,
		Enabled:      view.Enabled,
		NodeID:       view.NodeID,
		Node:         autoExecutionNodeOf(view.Node),
		Limit:        view.Limit,
		Ready:        view.Ready,
		Reasons:      append([]string{}, view.Reasons...),
		Detail:       view.Detail,
		ServeRunning: view.ServeRunning,
		Applied:      view.Applied,
		Reason:       view.Reason,
		LastAttempt:  view.LastAttempt,
	}
	out.Nodes = make([]httpresources.AutoExecutionNode, 0, len(view.Nodes))
	for _, n := range view.Nodes {
		out.Nodes = append(out.Nodes, autoExecutionNodeOf(n))
	}
	return out
}

func autoExecutionNodeOf(n AutoExecutionNode) httpresources.AutoExecutionNode {
	return httpresources.AutoExecutionNode{
		ID: n.ID, Label: n.Label, Ready: n.Ready, Reason: n.Reason, Detail: n.Detail, Slots: n.Slots,
	}
}

// systemAgentPort 把 Router 配置接到应用用例。
type systemAgentPort struct{}

func (systemAgentPort) Get(_ context.Context, projectID string) (httpresources.SystemAgentConfig, error) {
	view, err := SystemAgentGet(projectID)
	if err != nil {
		return httpresources.SystemAgentConfig{}, err
	}
	return systemAgentConfigOf(view), nil
}

func (systemAgentPort) Update(_ context.Context, req httpresources.SystemAgentSetRequest) (httpresources.SystemAgentConfig, error) {
	view, err := SystemAgentUpdate(SystemAgentUpdateInput{
		ProjectID:    req.ProjectID,
		Enabled:      req.Enabled,
		LLMNode:      req.LLMNode,
		WorkerNode:   req.WorkerNode,
		LLMCommand:   req.LLMCommand,
		AutoKinds:    req.AutoKinds,
		OpsAllowlist: req.OpsAllowlist,
		Actor:        req.Actor,
	})
	if err != nil {
		return httpresources.SystemAgentConfig{}, err
	}
	return systemAgentConfigOf(view), nil
}

func systemAgentConfigOf(view SystemAgentView) httpresources.SystemAgentConfig {
	return httpresources.SystemAgentConfig{
		ProjectID:        view.ProjectID,
		Enabled:          view.Enabled,
		LLMNode:          view.LLMNode,
		WorkerNode:       view.WorkerNode,
		LLMCommand:       view.LLMCommand,
		AutoKinds:        view.AutoKinds,
		OpsAllowlist:     view.OpsAllowlist,
		EscalatePatterns: view.EscalatePatterns,
		PollIntervalSec:  view.PollIntervalSec,
		MaxConcurrent:    view.MaxConcurrent,
		CooldownSec:      view.CooldownSec,
		MaxAttempts:      view.MaxAttempts,
		LLMReady:         view.LLMReady,
	}
}
