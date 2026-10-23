package workspace

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/home"
)

// Mode 是目录准备方式。
type Mode string

// 新建目录与复用已有目录。
const (
	ModeCreate Mode = "create"
	ModeReuse  Mode = "reuse"
)

// WorktreesDir 是 Git 工程的默认工作空间根目录。
const WorktreesDir = ".worktrees"

// Request 是一次工作空间准备的输入。
//
// 默认仍按 Agent 复用目录：Path 留空时目录固定为 <repo>/.worktrees/<agent>，
// 下一题沿用同一目录，而不是每题新开一个——目录数量不能随任务数增长。
type Request struct {
	ProjectID  model.ProjectID  `json:"project_id"`
	Instance   model.InstanceID `json:"task_instance"`
	RunID      model.RunID      `json:"run_id"`
	Generation model.Generation `json:"generation"`
	Agent      model.AgentID    `json:"agent_id"`

	// RepoRoot 是工程根（绝对路径）。留空时从 Cwd 推断。
	RepoRoot string `json:"repo_root,omitempty"`
	// Cwd 只在 RepoRoot 留空时用于推断工程根。
	Cwd string `json:"cwd,omitempty"`
	// Path 显式指定工作目录；留空表示沿用每 Agent 持久目录。
	Path string `json:"path,omitempty"`
	// Branch 是任务分支，必填：不猜分支名，猜错会检出别人的东西。
	Branch string `json:"branch"`
	// MainBranch 是主干分支名；留空按 main 处理。
	MainBranch string `json:"main_branch,omitempty"`
	// Base 是基线 ref；留空时优先 origin/<main>，退回 <main>。
	Base string `json:"base,omitempty"`
}

// Plan 是一次准备的冻结计划：目录、分支与基线提交在动目录之前就固定下来。
//
// 冻结基线的意义是「可回溯」：后面 reset／checkout 都用这个提交，
// 而不是用一个会随 fetch 漂移的分支名。
type Plan struct {
	ProjectID  model.ProjectID  `json:"project_id"`
	Instance   model.InstanceID `json:"task_instance"`
	RunID      model.RunID      `json:"run_id"`
	Generation model.Generation `json:"generation"`
	Agent      model.AgentID    `json:"agent_id"`

	// Repo 是规范化后的工程根；Path 是规范化后的工作目录。
	Repo   string `json:"repo_root"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Baseline 是冻结的 40 位基线提交。
	Baseline model.Commit `json:"baseline_commit"`
	// Existing 报告计划时目录是否已存在（存在就要走复用判定，不存在才新建）。
	Existing bool `json:"existing"`
}

// Freeze 只读地冻结计划：解析工程根、规范化目录、校验分支、把基线固定成提交。
//
// 它不检查占用、不建目录、不跑 reset——这些都在 Prepare 里按固定顺序发生。
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

	repo, err := resolveRepo(req)
	if err != nil {
		return Plan{}, err
	}
	plan.Repo = repo

	path, err := resolvePath(req, repo)
	if err != nil {
		return Plan{}, err
	}
	plan.Path = path
	plan.Branch = strings.TrimSpace(req.Branch)

	baseline, err := resolveBaseline(repo, req.Base, req.MainBranch)
	if err != nil {
		return Plan{}, err
	}
	plan.Baseline = baseline

	plan.Existing = dirExists(path)
	return plan, nil
}

// DefaultPath 返回每 Agent 的持久工作目录。
func DefaultPath(repo string, agent model.AgentID) string {
	return filepath.Join(repo, WorktreesDir, string(agent))
}

func validateRequest(req Request) error {
	if !req.ProjectID.Valid() {
		return invalidSpec("workspace.project_id 必须是 16 位小写十六进制，收到 %q", req.ProjectID)
	}
	if !req.Instance.Valid() {
		return invalidSpec("workspace.task_instance 必须形如 ti_<小写字母数字>，收到 %q", req.Instance)
	}
	if !req.RunID.Valid() {
		return invalidSpec("workspace.run_id 必须形如 run_<小写字母数字>，收到 %q", req.RunID)
	}
	if !req.Generation.Valid() {
		return invalidSpec("workspace.generation 从 1 开始，收到 %d", req.Generation)
	}
	if strings.TrimSpace(req.RepoRoot) == "" && strings.TrimSpace(req.Cwd) == "" {
		return invalidSpec("workspace.repo_root 与 workspace.cwd 至少要给一个")
	}
	if req.Path == "" && !req.Agent.Valid() {
		// 没有显式目录时才靠 Agent 命名；Agent 名会进文件路径，形态不对就是路径注入。
		return invalidSpec("workspace.agent_id 非法，无法推导默认目录：%q", req.Agent)
	}
	if req.Path != "" && !filepath.IsAbs(req.Path) {
		return invalidSpec("workspace.path 必须是绝对路径，收到 %q", req.Path)
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		return invalidSpec("workspace.branch 必填：不替调用方猜分支名")
	}
	if err := validateBranch(branch); err != nil {
		return err
	}
	return nil
}

// validateBranch 拦住会被 git 当成参数或非法 ref 的写法（分支名来自外部输入）。
func validateBranch(branch string) error {
	if strings.HasPrefix(branch, "-") {
		return invalidSpec("workspace.branch 不得以 - 开头（会被 git 当作选项）：%q", branch)
	}
	if strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".lock") || strings.HasSuffix(branch, ".") {
		return invalidSpec("workspace.branch 不是合法分支名：%q", branch)
	}
	return nil
}

func resolveRepo(req Request) (string, error) {
	root := strings.TrimSpace(req.RepoRoot)
	if root == "" {
		var err error
		root, err = home.RepoRoot(strings.TrimSpace(req.Cwd))
		if err != nil {
			return "", model.PortFailuref(model.PortErrInvalidSpec,
				"workspace.repo_root 无法从 %q 推断工程根：%v", req.Cwd, err)
		}
	}
	if !filepath.IsAbs(root) {
		return "", invalidSpec("workspace.repo_root 必须是绝对路径，收到 %q", root)
	}
	repo, err := CanonicalPath(root)
	if err != nil {
		return "", model.PortFailuref(model.PortErrInvalidSpec,
			"workspace.repo_root 规范化失败：%v", err)
	}
	if !dirExists(repo) {
		return "", invalidSpec("workspace.repo_root 不是目录：%q", repo)
	}
	if _, err := home.Git(repo, "rev-parse", "--git-common-dir"); err != nil {
		return "", invalidSpec("workspace.repo_root 不是 Git 仓库：%q", repo)
	}
	return repo, nil
}

func resolvePath(req Request, repo string) (string, error) {
	raw := req.Path
	if raw == "" {
		raw = DefaultPath(repo, req.Agent)
	}
	path, err := CanonicalPath(raw)
	if err != nil {
		return "", model.PortFailuref(model.PortErrInvalidSpec, "workspace.path 规范化失败：%v", err)
	}
	// 目录落在工程外也要允许（显式给了绝对路径就是调用方的决定），
	// 但不能是工程根本身：那会把主干直接 reset 掉。
	if path == repo {
		return "", invalidSpec("workspace.path 不得指向工程根 %q", repo)
	}
	return path, nil
}

// resolveBaseline 把基线固定成 40 位提交；解析不出来就失败，不回退到「随便一个分支」。
func resolveBaseline(repo, base, mainBranch string) (model.Commit, error) {
	var refs []string
	if b := strings.TrimSpace(base); b != "" {
		refs = []string{b}
	} else {
		main := strings.TrimSpace(mainBranch)
		if main == "" {
			main = "main"
		}
		refs = []string{"origin/" + main, main}
	}
	for _, ref := range refs {
		out, err := home.Git(repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil {
			continue
		}
		commit := model.Commit(out)
		if commit.Valid() {
			return commit, nil
		}
	}
	return "", invalidSpec("workspace.base 无法解析成提交：%s", strings.Join(refs, " / "))
}

// CanonicalPath 把路径收敛成「同一目录只有一个写法」：绝对化、清理 .／..／重复分隔符、
// 解析符号链接（尾段不存在时解析最近的存在祖先）。
//
// 独占检查必须用这个结果做键：/repo/.worktrees/a、/repo/.worktrees/a/、
// /repo/x/../.worktrees/a 与符号链接别名指的都是同一个目录，各算一个键就等于没检查。
func CanonicalPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", model.PortFailure(model.PortErrInvalidSpec, "路径为空，无法规范化")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return resolveSymlinks(filepath.Clean(abs)), nil
}

// resolveSymlinks 逐级向上找最近的存在祖先，解析它再把不存在的尾段接回去。
func resolveSymlinks(p string) string {
	rest := make([]string, 0, 4)
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			parts := append([]string{real}, reverse(rest)...)
			return filepath.Join(parts...)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func invalidSpec(format string, args ...any) error {
	return model.PortFailuref(model.PortErrInvalidSpec, format, args...)
}
