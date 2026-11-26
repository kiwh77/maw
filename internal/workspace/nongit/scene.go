package nongit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/workspace"
)

// Scene 是动目录之前的只读现场：存不存在、是不是目录、里面已有什么、
// 以及是否落在了某个 Git 工作树里。
//
// 采集只做 stat/readdir 和一次 rev-parse，不写任何文件——失败时把它随错误一起
// 交回，人工据此判断是「被占用」还是「名字已被别的文件占了」。非 Git 目录没有
// git status 可看，所以现场的证据就是第一层条目名。
type Scene struct {
	Path string `json:"path"`
	// GitToplevel 非空表示目录（或其最近的存在祖先）落在某个 Git 工作树里。
	// 这种目录必须走 Git 工作空间：非 Git 目录实现没有基线提交，
	// 接管仓库内文档就等于放弃了可回溯。
	GitToplevel string `json:"git_toplevel,omitempty"`
	Exists      bool   `json:"exists"`
	// IsDir 报告存在时的类型；存在但不是目录说明名字被一个文件占了，不能覆盖。
	IsDir bool `json:"is_dir,omitempty"`
	// Entries 是第一层条目名（排序后）。复用判定的证据：这些产物没有被覆盖。
	Entries    []string  `json:"entries,omitempty"`
	CapturedAt time.Time `json:"captured_at"`
}

// Empty 报告现场是一个空目录（只有空目录才算「建到一半的残留」）。
func (s Scene) Empty() bool { return s.Exists && s.IsDir && len(s.Entries) == 0 }

// IsForeign 报告「这个名字已经存在但不是目录」。这种现场一律不动：
// 用文件占位通常意味着有人在别处引用了它，覆盖或删除都可能毁掉别人的东西。
func (s Scene) IsForeign() bool { return s.Exists && !s.IsDir }

// InGit 报告目录落在 Git 工作树内（仓库内文档归 Git 工作空间管）。
func (s Scene) InGit() bool { return s.GitToplevel != "" }

// capture 只读采集现场；目录不存在时仍会探测祖先是否属于某个 Git 工作树，
// 这样「产物根不是 Git、但任务目录落在了嵌套仓库里」也能被认出来。
func capture(path string) Scene {
	scene := Scene{Path: path, CapturedAt: time.Now()}
	scene.GitToplevel = gitToplevel(path)
	st, err := os.Stat(path)
	if err != nil {
		return scene
	}
	scene.Exists = true
	scene.IsDir = st.IsDir()
	if !scene.IsDir {
		return scene
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return scene
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	scene.Entries = names
	return scene
}

// gitToplevel 返回该路径所属 Git 工作树的顶层目录；不属于任何工作树时返回空串。
//
// 路径可能还不存在（默认任务目录在首次准备前就有路径），所以先走到最近的存在
// 祖先再问 git——只对存在的目录跑 rev-parse 会漏掉「准备前就落在仓库里」这种情况。
func gitToplevel(path string) string {
	dir := nearestExistingDir(path)
	if dir == "" {
		return ""
	}
	out, err := home.Git(dir, "rev-parse", "--path-format=absolute", "--show-toplevel")
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	if top, err := workspace.CanonicalPath(out); err == nil {
		return top
	}
	return strings.TrimSpace(out)
}

// nearestExistingDir 返回最近的存在目录；路径本身是文件时返回它的父目录。
func nearestExistingDir(path string) string {
	cur := filepath.Clean(path)
	for {
		if st, err := os.Stat(cur); err == nil {
			if st.IsDir() {
				return cur
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				return ""
			}
			return parent
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}
