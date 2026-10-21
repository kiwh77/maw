# maw — Multi-Agent Worktree CLI (Go)

单二进制命令行：在任意 git 工程中用蓝图管理多 agent 并行开发（worktree 隔离、范围检查、合并队列、看板）。

设计对照见 [docs/DESIGN.md](docs/DESIGN.md)。Python 原型保留在 `legacy/python/`。

## 安装（单二进制）

需要 Go 1.22+：

```bash
./install.sh
# 安装到 ~/.local/bin/maw（CGO_ENABLED=0，可跨平台交叉编译）
maw --version
```

手动构建：

```bash
CGO_ENABLED=0 go build -o maw ./cmd/maw
```

交叉编译示例：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o maw-linux-amd64 ./cmd/maw
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o maw-darwin-arm64 ./cmd/maw
```

## 快速上手

```bash
cd /path/to/your-git-repo
maw init
maw plan apply .multiagent/blueprint.toml --yes

export MAW_AGENT=w1
maw next
# 进入输出的 worktree，改 write_scope 内文件并 commit
maw submit

export MAW_AGENT=int
maw merge --yes
maw board   # → .multiagent/board/index.html
```

## 命令

| 命令 | 说明 |
| --- | --- |
| `maw init` | 状态库、协议段、hooks |
| `maw plan apply / check` | 蓝图校验与落盘 |
| `maw next` | 领取任务 + worktree |
| `maw submit` | 范围检查 → 合并队列 |
| `maw merge --yes` | integrator 串行合入 |
| `maw board` | 终端 + HTML 看板 |

附属：`progress`、`doctor`、`review`、`block`、`task`、`hook`。全局：`--json`、`--dry-run`、`-C`。

## 测试

```bash
go test ./...
```

## 技术栈

- Go + [cobra](https://github.com/spf13/cobra)
- 纯 Go SQLite：[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)（无 CGO）
- TOML：pelletier/go-toml
