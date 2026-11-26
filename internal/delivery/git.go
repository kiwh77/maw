package delivery

import (
	"fmt"
	"os"
	"strings"

	"github.com/kiwh77/maw/internal/home"
)

// git 是冻结交付所需的只读 git 查询。
//
// 只读：本包不做 checkout／reset／merge。目录怎么变是工作空间的事（VN-501），
// 这里只回答「这个对象在不在」「基线是不是祖先」「某个提交里这份文件长什么样」。
type git interface {
	// hasCommit 报告某个提交对象是否存在于仓库。
	hasCommit(rev string) bool
	// isAncestor 报告 base 是否是 tip 的祖先（含相等）。
	isAncestor(base, tip string) bool
	// changedFiles 列出 base..tip 之间改动的文件路径（相对仓库根）。
	changedFiles(base, tip string) ([]string, error)
	// fileAt 读取 rev:path 的内容。
	fileAt(rev, path string) ([]byte, error)
}

// gitCmd 是 git 的实现：全部走 `git -C <repo>`，不依赖调用者的当前目录。
type gitCmd struct {
	repo string
}

func (g gitCmd) hasCommit(rev string) bool {
	if strings.TrimSpace(rev) == "" {
		return false
	}
	if _, err := home.Git(g.repo, "cat-file", "-e", rev+"^{commit}"); err != nil {
		return false
	}
	return true
}

func (g gitCmd) isAncestor(base, tip string) bool {
	if base == tip {
		return true
	}
	_, err := home.Git(g.repo, "merge-base", "--is-ancestor", base, tip)
	return err == nil
}

func (g gitCmd) changedFiles(base, tip string) ([]string, error) {
	out, err := home.Git(g.repo, "diff", "--name-only", base+".."+tip)
	if err != nil {
		return nil, fmt.Errorf("列出 %s..%s 的改动失败：%w（%s）", base, tip, err, out)
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if p := strings.TrimSpace(line); p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func (g gitCmd) fileAt(rev, path string) ([]byte, error) {
	out, err := home.Git(g.repo, "show", rev+":"+path)
	if err != nil {
		return nil, fmt.Errorf("读取 %s:%s 失败：%w", rev, path, err)
	}
	return []byte(out), nil
}

// readWorktreeFile 读工作目录里的文件（只在提交里没有时才用）。
func readWorktreeFile(dir, rel string) ([]byte, bool) {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(rel) == "" {
		return nil, false
	}
	raw, err := os.ReadFile(strings.TrimRight(dir, "/") + "/" + strings.TrimLeft(rel, "/"))
	if err != nil {
		return nil, false
	}
	return raw, true
}
