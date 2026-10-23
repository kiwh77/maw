package workspace

import (
	"os"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/home"
)

// Scene 是准备前对该目录的只读现场：存在与否、属于哪个仓库、头指针与脏文件。
//
// 失败时把它随错误一起交回，人工据此判断是「被谁挡住」还是「目录已脏」。
// 采集只跑 rev-parse／status，不写 .git，也不动工作区。
type Scene struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Empty  bool   `json:"empty,omitempty"`
	// Entries 是目录第一层的条目数；拒绝非工作树目录时用它说明「里面已经有东西」。
	Entries   int    `json:"entries,omitempty"`
	Toplevel  string `json:"toplevel,omitempty"`
	CommonDir string `json:"common_dir,omitempty"`
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Detached  bool   `json:"detached,omitempty"`
	// Dirty 是 git status --porcelain 的原始条目（含未跟踪）。
	Dirty []string `json:"dirty,omitempty"`
	// TrackedDirty 是已跟踪文件的改动：reset --hard 会丢掉的部分。
	TrackedDirty []string  `json:"tracked_dirty,omitempty"`
	CapturedAt   time.Time `json:"captured_at"`
}

// IsWorktreeRoot 报告目录本身就是一个 Git 工作树根。
//
// 只跑 rev-parse --show-toplevel 不够：工程里的普通子目录同样会解析出工程根。
// 那种目录不是工作树，对它 reset --hard 等于把整块工程工作区一起重置，
// 所以判据必须是「解析结果就是自己」。
func (s Scene) IsWorktreeRoot() bool {
	if !s.Exists || s.Toplevel == "" {
		return false
	}
	return s.Toplevel == s.Path
}

// IsForeign 报告目录存在但不是本工程的工作树根（普通目录、别人的工作树、
// 或工程里的子目录都算）。这种目录一律不复用、不重置。
func (s Scene) IsForeign() bool { return s.Exists && !s.IsWorktreeRoot() }

// capture 只读采集现场；目录不存在时返回零值现场。
func capture(path string) Scene {
	scene := Scene{Path: path, CapturedAt: time.Now()}
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		return scene
	}
	scene.Exists = true
	if entries, err := os.ReadDir(path); err == nil {
		scene.Entries = len(entries)
		scene.Empty = len(entries) == 0
	}
	top, err := home.Git(path, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil || strings.TrimSpace(top) == "" {
		return scene
	}
	if canonical, err := CanonicalPath(top); err == nil {
		scene.Toplevel = canonical
	} else {
		scene.Toplevel = top
	}
	scene.CommonDir, _ = home.Git(path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	scene.Head, _ = home.Git(path, "rev-parse", "--verify", "--quiet", "HEAD")
	if branch, err := home.Git(path, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		scene.Branch = branch
	} else {
		scene.Detached = true
	}
	scene.Dirty, scene.TrackedDirty = dirtyEntries(path)
	return scene
}

// dirtyEntries 拆出 porcelain 条目：以 ?? 开头的是未跟踪，其余是已跟踪改动。
func dirtyEntries(path string) (all, tracked []string) {
	raw, err := home.Git(path, "status", "--porcelain", "--untracked-files=all")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		all = append(all, ln)
		if !strings.HasPrefix(ln, "??") {
			tracked = append(tracked, ln)
		}
	}
	return all, tracked
}

// gitCommonDir 返回仓库/worktree 的公共 git 目录（规范化后的绝对路径）。
func gitCommonDir(dir string) (string, error) {
	out, err := home.Git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return CanonicalPath(out)
}

// isOwnWorktree 判断目录是否属于该工程（是工作树根，且公共 git 目录相同）。
func isOwnWorktree(scene Scene, repo string) bool {
	if !scene.IsWorktreeRoot() || scene.CommonDir == "" {
		return false
	}
	repoCommon, err := gitCommonDir(repo)
	if err != nil {
		return false
	}
	own, err := CanonicalPath(scene.CommonDir)
	if err != nil {
		return false
	}
	return own == repoCommon
}
