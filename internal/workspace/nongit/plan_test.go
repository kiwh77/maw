package nongit_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/workspace"
	"github.com/kiwh77/maw/internal/workspace/nongit"
)

// nonGitRoot 建一个确认不在任何 Git 工作树里的临时产物根。
//
// 注意不能直接用 t.TempDir() 的返回值做断言：macOS 的 /var 是 /private/var 的
// 符号链接，本包按解析后的真实路径冻结计划，所以这里统一用规范化结果。
func nonGitRoot(t *testing.T) string {
	t.Helper()
	root, err := workspace.CanonicalPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		t.Fatalf("测试目录 %q 落在 Git 工作树里（%s），换 TMPDIR 再跑", root, strings.TrimSpace(string(out)))
	}
	return root
}

// gitRun 在指定目录跑 git，失败即终止测试。
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo 建一个空仓库（不需要提交：rev-parse --show-toplevel 不依赖 HEAD）。
func initRepo(t *testing.T) string {
	t.Helper()
	repo := nonGitRoot(t)
	gitRun(t, repo, "init", "-b", "main")
	return repo
}

func baseRequest(root string) nongit.Request {
	return nongit.Request{
		ProjectID:  "0123456789abcdef",
		Instance:   "ti_abc123",
		RunID:      "run_abc123",
		Generation: 1,
		Agent:      "codebuddy-doc-1",
		Root:       root,
	}
}

func TestFreezeDefaultsToAgentDir(t *testing.T) {
	root := nonGitRoot(t)
	plan, err := nongit.Freeze(baseRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, nongit.ManagedDir, "codebuddy-doc-1")
	if plan.Path != want {
		t.Fatalf("默认任务目录应为 %q，得到 %q", want, plan.Path)
	}
	if plan.Layout != nongit.LayoutAgent {
		t.Fatalf("Layout 应为 %q，得到 %q", nongit.LayoutAgent, plan.Layout)
	}
	if plan.Existing {
		t.Fatalf("目录还不存在，Existing 不应为 true")
	}
}

func TestFreezeUsesCwdWhenRootEmpty(t *testing.T) {
	root := nonGitRoot(t)
	req := baseRequest("")
	req.Cwd = root
	plan, err := nongit.Freeze(req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Root != root {
		t.Fatalf("Cwd 应原样作为产物根 %q，得到 %q", root, plan.Root)
	}
}

func TestFreezeRequiresExplicitRoot(t *testing.T) {
	req := baseRequest("")
	_, err := nongit.Freeze(req)
	if err == nil {
		t.Fatal("既没给 root 也没给 cwd 时必须失败：非 Git 无法推断产物根")
	}
	if !strings.Contains(err.Error(), "必须显式给出") {
		t.Fatalf("错误应说明产物根必须显式给出，得到 %v", err)
	}
}

func TestFreezeRejectsMissingRoot(t *testing.T) {
	_, err := nongit.Freeze(baseRequest(filepath.Join(nonGitRoot(t), "nope")))
	if err == nil {
		t.Fatal("不存在的产物根必须失败")
	}
	if !strings.Contains(err.Error(), "不代建") {
		t.Fatalf("错误应说明不代建产物根，得到 %v", err)
	}
}

func TestFreezeRejectsNonAbsoluteRoot(t *testing.T) {
	_, err := nongit.Freeze(baseRequest("relative/dir"))
	if err == nil || !strings.Contains(err.Error(), "绝对路径") {
		t.Fatalf("相对路径的产物根必须失败，得到 %v", err)
	}
}

// 仓库内文档走 Git 工作空间：产物根落在任何 Git 工作树里都要拒绝，而不只是工程根。
func TestFreezeRejectsGitRoot(t *testing.T) {
	repo := initRepo(t)
	for name, root := range map[string]string{
		"仓库根": repo,
		"仓库子目录": func() string {
			sub := filepath.Join(repo, "docs")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}
			return sub
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := nongit.Freeze(baseRequest(root))
			if err == nil {
				t.Fatalf("%q 在 Git 工作树里，必须拒绝", root)
			}
			if !strings.Contains(err.Error(), "Git 工作空间") {
				t.Fatalf("错误应指回 Git 工作空间，得到 %v", err)
			}
			if !errors.Is(err, model.PortFailure(model.PortErrInvalidSpec, "")) {
				t.Fatalf("应归类为 invalid_spec，得到 %v", err)
			}
		})
	}
}

func TestFreezeExplicitPathWithinRoot(t *testing.T) {
	root := nonGitRoot(t)
	req := baseRequest(root)
	req.Path = filepath.Join(root, "reports", "q3-draft")
	plan, err := nongit.Freeze(req)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Layout != nongit.LayoutExplicit {
		t.Fatalf("显式路径的 Layout 应为 %q，得到 %q", nongit.LayoutExplicit, plan.Layout)
	}
	if plan.Path != req.Path {
		t.Fatalf("计划路径应为 %q，得到 %q", req.Path, plan.Path)
	}
}

// 跨工程路径：另一个产物根下的目录不能被本工程接管。
func TestFreezeRejectsPathOutsideRoot(t *testing.T) {
	root := nonGitRoot(t)
	other := nonGitRoot(t)
	req := baseRequest(root)
	req.Path = filepath.Join(other, "shared", "draft")
	_, err := nongit.Freeze(req)
	if err == nil {
		t.Fatal("产物根之外的路径必须拒绝")
	}
	if !strings.Contains(err.Error(), "不在产物根") {
		t.Fatalf("错误应说明跨根目录不复用，得到 %v", err)
	}
}

func TestFreezeRejectsRootAsPath(t *testing.T) {
	root := nonGitRoot(t)
	req := baseRequest(root)
	req.Path = root
	_, err := nongit.Freeze(req)
	if err == nil || !strings.Contains(err.Error(), "不得指向产物根") {
		t.Fatalf("任务目录等于产物根时必须失败，得到 %v", err)
	}
}

// 别名路径必须收敛成同一个键，否则占用检查会被 ./ 与 .. 绕过去。
func TestFreezeCanonicalizesAliases(t *testing.T) {
	root := nonGitRoot(t)
	want, err := nongit.Freeze(baseRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	aliases := []string{
		filepath.Join(root, nongit.ManagedDir, ".", "codebuddy-doc-1"),
		filepath.Join(root, nongit.ManagedDir, "codebuddy-doc-1") + string(filepath.Separator),
		filepath.Join(root, nongit.ManagedDir, "codebuddy-doc-1", "..", "codebuddy-doc-1"),
		filepath.Join(root, ".", nongit.ManagedDir, "codebuddy-doc-1"),
	}
	for _, alias := range aliases {
		req := baseRequest(root)
		req.Path = alias
		plan, err := nongit.Freeze(req)
		if err != nil {
			t.Fatalf("%q: %v", alias, err)
		}
		if plan.Path != want.Path {
			t.Fatalf("%q 应收敛成 %q，得到 %q", alias, want.Path, plan.Path)
		}
	}
}

func TestFreezeMarksExisting(t *testing.T) {
	root := nonGitRoot(t)
	if err := os.MkdirAll(nongit.DefaultPath(root, "codebuddy-doc-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := nongit.Freeze(baseRequest(root))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Existing {
		t.Fatal("目录已存在，Existing 应为 true")
	}
}

func TestFreezeValidatesIdentity(t *testing.T) {
	root := nonGitRoot(t)
	cases := map[string]func(*nongit.Request){
		"工程 ID": func(r *nongit.Request) { r.ProjectID = "xyz" },
		"任务实例":  func(r *nongit.Request) { r.Instance = "abc" },
		"运行 ID": func(r *nongit.Request) { r.RunID = "abc" },
		"代际":    func(r *nongit.Request) { r.Generation = 0 },
		"Agent": func(r *nongit.Request) { r.Agent = "" },
		"相对路径":  func(r *nongit.Request) { r.Path = "relative/x" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := baseRequest(root)
			mutate(&req)
			if _, err := nongit.Freeze(req); err == nil {
				t.Fatalf("%s 非法时必须失败", name)
			}
		})
	}
}
