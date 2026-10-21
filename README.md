# maw — Multi-Agent Worktree CLI

命令行应用：在任意 git 工程中用蓝图管理多 agent 并行开发（worktree 隔离、范围检查、合并队列、看板）。

设计文档见 [docs/DESIGN.md](docs/DESIGN.md)（v0.2）。

## 安装

```bash
./install.sh
# 确保 ~/.local/bin 在 PATH 中
maw --version
```

或：

```bash
export PYTHONPATH=/Users/wuhao/Documents/Projects/goal-agents/src
python3 -m maw --help
```

## 快速上手

```bash
cd /path/to/your-git-repo
maw init
# 编辑 .multiagent/blueprint.toml（或自备蓝图）
maw plan apply .multiagent/blueprint.toml --yes

# 各 worker 会话：
export MAW_AGENT=w1
maw next
cd "$(maw next --json | python3 -c 'import sys,json; print(json.load(sys.stdin)["worktree"])')"
# …实现 write_scope 内改动并 commit…
maw progress -m "milestone"
maw submit

# 集成者：
export MAW_AGENT=int
maw merge --yes
maw board   # 同时写入 .multiagent/board/index.html
```

## M1 命令

| 命令 | 说明 |
| --- | --- |
| `maw init` | 状态库、协议段、hooks |
| `maw plan apply / check` | 蓝图校验与落盘 |
| `maw next` | 领取任务 + worktree |
| `maw submit` | 范围检查 → 评审/合并队列 |
| `maw merge --yes` | integrator 串行合入 |
| `maw board` | 终端 + HTML 看板 |

附属：`progress`、`doctor`、`review`、`block`、`task list/show/unblock`、`hook *`。

全局：`--json`、`--dry-run`。

## 测试

```bash
cd /Users/wuhao/Documents/Projects/goal-agents
PYTHONPATH=src python3 -m unittest discover -s tests -v
```

## 协议

`maw init` 向 `AGENTS.md` / `CLAUDE.md` / `.cursor/rules/maw.mdc` 写入带版本标记的协议段，并安装 `pre-commit` / `pre-push` hooks。
