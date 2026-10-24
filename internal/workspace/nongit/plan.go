package nongit

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/workspace"
)

// ManagedDir 是非 Git 工程下的受管工作目录名（架构文件布局
// `<non-git-project>/.maw-work/`）。放在产物根下而不是 ~/.maw 下，是因为
// 产物本身要跟着工程走：换机器、备份工程目录时要一起带走。
const ManagedDir = ".maw-work"

// Layout 说明任务目录是怎么定下来的。
type Layout string

const (
	// LayoutAgent 是默认形态 <产物根>/.maw-work/<agent>：同一 Agent 复用同一目录。
	LayoutAgent Layout = "agent"
	// LayoutExplicit 是调用方显式指定的任务目录，仍必须落在本工程产物根下。
	LayoutExplicit Layout = "explicit"
)

// Request 是一次非 Git 工作目录准备的输入。
type Request struct {
	ProjectID  model.ProjectID  `json:"project_id"`
	Instance   model.InstanceID `json:"task_instance"`
	RunID      model.RunID      `json:"run_id"`
	Generation model.Generation `json:"generation"`
	Agent      model.AgentID    `json:"agent_id"`

	// Root 是非 Git 产物根（绝对路径），必须已存在。
	// 非 Git 没有仓库根可推断，所以这里不搜索、不猜。
	Root string `json:"root,omitempty"`
	// Cwd 在 Root 留空时原样作为产物根（不向上搜索）。
	Cwd string `json:"cwd,omitempty"`
	// Path 显式指定任务目录；留空表示 <Root>/.maw-work/<Agent>。
	Path string `json:"path,omitempty"`
}

// Plan 是动目录之前冻结的计划：产物根与任务目录都收敛成唯一写法。
//
// 冻结的目的是让后面的每一步都用同一个字符串：占用检查、mkdir、事后核对
// 都拿 plan.Path 当键，避免「检查的是 /a/b/、建的是 /a/b」这种绕过。
type Plan struct {
	ProjectID  model.ProjectID  `json:"project_id"`
	Instance   model.InstanceID `json:"task_instance"`
	RunID      model.RunID      `json:"run_id"`
	Generation model.Generation `json:"generation"`
	Agent      model.AgentID    `json:"agent_id"`

	// Root 是规范化后的产物根；Path 是规范化后的任务目录。
	Root   string `json:"root"`
	Path   string `json:"path"`
	Layout Layout `json:"layout"`
	// Existing 报告计划时任务目录是否已存在（存在就必然走「原地复用」）。
	Existing bool `json:"existing"`
}

// Freeze 只读地冻结计划：解析产物根、规范化任务目录、确认它没越出产物根。
//
// 它不检查占用、不建目录——这些在 Prepare 里按固定顺序发生。产物根落在 Git
// 工作树里直接失败：仓库内文档走 internal/workspace，那边才有基线提交与任务分支。
func Freeze(req Request) (Plan, error) {
	plan := Plan{
		ProjectID:  req.ProjectID,
		Instance:   req.Instance,
		RunID:      req.RunID,
		Generation: req.Generation,
		Agent:      req.Agent,
	}
	if err := validateRequest(req); err != nil {
		return Plan{}, err
	}

	root, err := resolveRoot(req)
	if err != nil {
		return Plan{}, err
	}
	plan.Root = root

	path, layout, err := resolvePath(req, root)
	if err != nil {
		return Plan{}, err
	}
	plan.Path = path
	plan.Layout = layout

	plan.Existing = dirExists(path)
	return plan, nil
}

// DefaultPath 返回每 Agent 的持久任务目录。
func DefaultPath(root string, agent model.AgentID) string {
	return filepath.Join(root, ManagedDir, string(agent))
}

func validateRequest(req Request) error {
	if !req.ProjectID.Valid() {
		return invalidSpec("nongit.project_id 必须是 16 位小写十六进制，收到 %q", req.ProjectID)
	}
	if !req.Instance.Valid() {
		return invalidSpec("nongit.task_instance 必须形如 ti_<小写字母数字>，收到 %q", req.Instance)
	}
	if !req.RunID.Valid() {
		return invalidSpec("nongit.run_id 必须形如 run_<小写字母数字>，收到 %q", req.RunID)
	}
	if !req.Generation.Valid() {
		return invalidSpec("nongit.generation 从 1 开始，收到 %d", req.Generation)
	}
	if path := strings.TrimSpace(req.Path); path != "" && !filepath.IsAbs(path) {
		return invalidSpec("nongit.path 必须是绝对路径，收到 %q", path)
	}
	if strings.TrimSpace(req.Path) == "" && !req.Agent.Valid() {
		// 没有显式目录时才靠 Agent 命名；Agent 名会进文件路径，形态不对就是路径注入。
		return invalidSpec("nongit.agent_id 非法，无法推导默认任务目录：%q", req.Agent)
	}
	return nil
}

// resolveRoot 解析产物根：必须显式、绝对、已存在，且不属于任何 Git 工作树。
func resolveRoot(req Request) (string, error) {
	raw := strings.TrimSpace(req.Root)
	if raw == "" {
		raw = strings.TrimSpace(req.Cwd)
	}
	if raw == "" {
		return "", invalidSpec("nongit.root 必填：非 Git 没有可推断的仓库根，产物根必须显式给出")
	}
	if !filepath.IsAbs(raw) {
		return "", invalidSpec("nongit.root 必须是绝对路径，收到 %q", raw)
	}
	root, err := workspace.CanonicalPath(raw)
	if err != nil {
		return "", model.PortFailuref(model.PortErrInvalidSpec, "nongit.root 规范化失败：%v", err)
	}
	if !dirExists(root) {
		return "", invalidSpec("nongit.root 不是已存在的目录：%q（产物根不代建：猜出来的根会把产物放到错的地方）", root)
	}
	if top := gitToplevel(root); top != "" {
		return "", invalidSpec("nongit.root %q 位于 Git 工作树 %s 内：仓库内文档走 Git 工作空间（internal/workspace），非 Git 目录不接管",
			root, top)
	}
	return root, nil
}

// resolvePath 定下任务目录，并保证它落在本工程产物根之下。
func resolvePath(req Request, root string) (string, Layout, error) {
	raw := strings.TrimSpace(req.Path)
	layout := LayoutExplicit
	if raw == "" {
		raw = DefaultPath(root, req.Agent)
		layout = LayoutAgent
	}
	path, err := workspace.CanonicalPath(raw)
	if err != nil {
		return "", "", model.PortFailuref(model.PortErrInvalidSpec, "nongit.path 规范化失败：%v", err)
	}
	if path == root {
		return "", "", invalidSpec("nongit.path 不得指向产物根本身 %q：那是工程目录，不是任务目录", root)
	}
	if !within(root, path) {
		return "", "", invalidSpec("nongit.path %q 不在产物根 %q 下：跨工程／跨根目录不复用，否则两个工程会写进同一份产物目录",
			path, root)
	}
	return path, layout, nil
}

// within 报告 path 是否严格位于 root 之下（root 本身不算）。
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func invalidSpec(format string, args ...any) error {
	return model.PortFailuref(model.PortErrInvalidSpec, format, args...)
}
