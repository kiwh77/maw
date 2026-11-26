package blueprint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseMatchesLoad 保证「内存校验」与「读盘校验」看到的是同一份模型：
// 入队路径用 Parse 校验候选全文，若两者解析不一致，校验就失去意义。
func TestParseMatchesLoad(t *testing.T) {
	content := []byte(`[meta]
name = "wave"
version = 2
main_branch = "main"

[[iteration]]
id = "IT-1"
name = "第一批"
status = "running"

[[task]]
id = "T-1"
title = "t"
goal = "现状：无。目标：有。"
priority = 10
iteration = "IT-1"
merge_order = 20
deps = []
write_scope = ["a/**"]
read_scope = ["README.md"]
acceptance = ["ok"]
verify = ["manual: ok"]
not_doing = ["none"]
`)
	path := filepath.Join(t.TempDir(), "blueprint.toml")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}

	fromDisk, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fromBytes, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromDisk.Tasks) != 1 || len(fromBytes.Tasks) != 1 {
		t.Fatalf("任务数不一致：%d vs %d", len(fromDisk.Tasks), len(fromBytes.Tasks))
	}
	diskTask, byteTask := fromDisk.Tasks[0], fromBytes.Tasks[0]
	if diskTask.ID != byteTask.ID || diskTask.Iteration != byteTask.Iteration ||
		diskTask.Priority != byteTask.Priority || diskTask.MergeOrder != byteTask.MergeOrder {
		t.Fatalf("解析结果不一致：%+v vs %+v", diskTask, byteTask)
	}
	if fromBytes.Meta.MainBranch != "main" || fromBytes.Meta.Version != 2 {
		t.Fatalf("meta 解析不对：%+v", fromBytes.Meta)
	}
	if _, err := Parse([]byte("[[task]\n")); err == nil {
		t.Fatal("坏 TOML 必须报错")
	}
}
