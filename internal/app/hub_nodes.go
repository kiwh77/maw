package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	httpresources "github.com/kiwh77/maw/internal/httpapi/resources"
	"github.com/kiwh77/maw/internal/nodes"
)

// hubDBNodes 把 HTTP 节点写口接到本机 nodes 表（通用 local_cli）。
type hubDBNodes struct {
	gdb string
}

func (h hubDBNodes) Register(_ context.Context, req httpresources.NodeWriteRequest) (httpresources.NodeWriteResult, error) {
	return h.upsert(req, true)
}

func (h hubDBNodes) UpdateConfig(_ context.Context, req httpresources.NodeWriteRequest) (httpresources.NodeWriteResult, error) {
	return h.upsert(req, false)
}

func (h hubDBNodes) upsert(req httpresources.NodeWriteRequest, createOnly bool) (httpresources.NodeWriteResult, error) {
	reg := req.Registration
	id := strings.TrimSpace(string(reg.Config.NodeID))
	if id == "" || !model.NodeID(id).Valid() {
		return httpresources.NodeWriteResult{}, httpresources.ErrInvalid{
			Code: "invalid_node_id", Message: "node_id 非法",
		}
	}
	label := strings.TrimSpace(reg.Config.Name)
	if label == "" {
		label = id
	}
	slots := reg.Config.SlotLimit
	if slots <= 0 {
		slots = 1
	}
	argv := argvFromRegistration(reg)
	meta := localCLINodeMeta(argv, false)
	if reg.Config.EntryPath != "" {
		meta["entry_path"] = reg.Config.EntryPath
	}
	if string(reg.Config.Adapter) != "" {
		meta["adapter_kind"] = string(reg.Config.Adapter)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var created bool
	err := db.WithTx(h.gdb, func(tx *sql.Tx) error {
		existing, e := db.GetNode(tx, id)
		if e != nil {
			return e
		}
		created = existing == nil
		if !createOnly && existing == nil {
			return nodes.ErrNotFound
		}
		return db.UpsertNode(tx, &db.Node{
			ID: id, ProjectID: reg.ProjectID, Label: label, SlotLimit: slots,
			CreatedAt: now, LastSeen: now, Meta: meta,
		})
	})
	if err != nil {
		return httpresources.NodeWriteResult{}, err
	}
	cfg := reg.Config
	if cfg.NodeID == "" {
		cfg.NodeID = model.NodeID(id)
	}
	if cfg.Name == "" {
		cfg.Name = label
	}
	if cfg.Mode == "" {
		cfg.Mode = model.NodeManagedCLI
	}
	if cfg.SlotLimit <= 0 {
		cfg.SlotLimit = slots
	}
	if cfg.Adapter == "" {
		cfg.Adapter = model.AdapterLocalCLI
	}
	snap := nodes.Snapshot{
		NodeID: model.NodeID(id), ProjectID: reg.ProjectID, Config: cfg,
		Revision: 1, Status: nodes.UnknownStatus(),
		Admission: nodes.AdmissionOpen, FreeSlots: slots,
	}
	return httpresources.NodeWriteResult{Snapshot: snap, Created: created}, nil
}

func argvFromRegistration(reg nodes.Registration) []string {
	if reg.Config.EntryPath == "" {
		return nil
	}
	argv := []string{reg.Config.EntryPath}
	// ProfileRef 可承载 JSON argv 尾部；没有就只留可执行文件。
	raw := strings.TrimSpace(reg.Config.ProfileRef)
	if raw == "" {
		return argv
	}
	var extra []string
	if err := json.Unmarshal([]byte(raw), &extra); err == nil && len(extra) > 0 {
		return append(argv, extra...)
	}
	return append(argv, raw)
}
