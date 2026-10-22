---
name: maw-init
description: >-
  在 git 工程中初始化或升级 maw 多 Agent 环境（maw init、config、协议段、hooks、doctor）。
  Use when the user asks to maw init、初始化 maw、接入 maw、升级 maw 协议、
  .multiagent 还不存在、doctor 提示未 init、或首次把仓库挂进 maw 看板。
---

# maw 工程初始化（init）

在**仓库根目录**（git root）操作。总览见 [maw](../maw/SKILL.md)。  
下一步通常：[maw-task-list](../maw-task-list/SKILL.md) 拆清单 → [maw-plan-push](../maw-plan-push/SKILL.md) 压入。

## 何时用

- 新仓库首次接入多 Agent worktree 编排
- `maw doctor` 报「工程未 init」
- 无 `.multiagent/` 或协议段过旧（需 `maw init --upgrade`）
- 用户说「初始化 maw」「接入看板」

## 前置

```bash
which maw && maw --version   # 建议 >= 0.3.1；缺失则在本仓库 ./install.sh
git rev-parse --show-toplevel
```

非 git 仓库先 `git init`（或换到正确工程）。全局状态在 `~/.maw/maw.db`（可用 `MAW_GLOBAL` 覆盖）。

## 工作流

```text
[1] 预检 → [2] maw init → [3] 看 config 占位 → [4] doctor → [5] 交棒拆清单（在清单里定主干）
```

### 1) 预检

```bash
maw doctor          # 已 init 则 OK；未 init 会提示下一步
ls .multiagent 2>/dev/null || echo "need init"
```

若已 init 且用户只要升级协议/默认配置：

```bash
maw init --upgrade
```

### 2) 初始化

```bash
maw init
# 可选预览：maw init --dry-run
```

**会创建/登记**

| 产物 | 说明 |
|---|---|
| `~/.maw/maw.db` | 全局任务/Agent/工程库 |
| `.multiagent/` | 工程本地：config、blueprint、board、handoffs |
| `.multiagent/config.toml` | 主干、并行度、审批、identity |
| `.multiagent/blueprint.toml` | 示例蓝图（随后由 maw-plan-push 重写） |
| `AGENTS.md` 等协议段 | `<!-- maw:begin v3 -->` …（勿手改标记内正文） |
| git hooks | pre-commit / pre-push 等（范围与主干保护） |

### 3) 查看 `.multiagent/config.toml`（占位即可）

`init` 会探测并写入临时 `main_branch`（常为 `main`/`master`），**不是最终权威**。  
正式主干在 **[maw-task-list](../maw-task-list/SKILL.md)** 文首指定，经 **[maw-plan-push](../maw-plan-push/SKILL.md)** 写入蓝图 `[meta] main_branch`，`plan apply`（≥0.3.9）再同步回本文件。

```toml
[project]
main_branch = "main"          # init 探测占位；真正主干以任务清单 / 蓝图 meta 为准
merge_strategy = "squash"
max_parallel = 0              # 看板参考；0=不展示。领取不卡槽（≥0.3.6）
blueprint = ".multiagent/blueprint.toml"

[approvals]
plan = "manual"               # plan apply 需 --yes
merge = "manual"
review = "auto"

[identity]
# Worker 名推荐动态注册：{应用}-{工程}-{序号}，如 cursor-demo-1
workers = []
reviewers = ["r1"]
integrator = "int"
```

- 勿在 init 阶段假设永远合入 `main`；集成分支 / `dev` 等在拆清单时写清。
- `max_parallel` 仅参考，不限制领取；并行度由写盘重叠与 deps 自然约束。
- Worker 名统一为 `{应用}-{工程slug}-{序号}`（工程名去连字符小写）；勿再用 `w1` / 业务绰号。
- 勿把密钥写进 config。

### 4) 验收

```bash
maw doctor
maw board                 # 应出现本工程；任务可能仍为 0
ls .multiagent/config.toml .multiagent/blueprint.toml
```

协议文件应含 `maw:begin` 标记（常见：`AGENTS.md`、`.cursor/rules/maw.mdc` 等，视检测到的工具而定）。

### 5) 交棒

初始化**不**等于有可领任务。下一步：

1. 用 **maw-task-list** 拆清单（文首写明主分支）
2. 用 **maw-plan-push** 写蓝图（`meta.main_branch`）并 `plan apply`
3. `maw register && maw next`（自动分配如 `cursor-demo-1`）
4. 可选：`maw board --serve`

写一句 `.multiagent/STATUS.md`（建议）：

```markdown
# maw 状态
- init：OK · maw x.y.z · project_id=…
- 下一步：maw-plan-push 压入蓝图
```

## 硬规则

1. 只在目标仓库根执行；勿在 worktree 子目录误 init 成新工程。
2. **不要手改** `<!-- maw:begin` … `maw:end -->` 内协议正文；升级用 `maw init --upgrade`。
3. init 不会合并代码、不会 apply 业务蓝图；示例 blueprint 仅占位。
4. 已有自定义 `config.toml` / `blueprint.toml` 时：普通 `maw init` **不覆盖**已存在文件；`--upgrade` 会重写 **config** 默认模板——升级前先 diff/备份。
5. submit 后可自行 `maw merge --yes`；禁止直接 git merge/rebase/push 主干。合入后若仍有 ready，继续 `maw next`。

## 输出给用户（中文）

```markdown
**maw init**
- 工程：`<repo>` · project_id=`…`
- 全局库：`~/.maw/maw.db`
- 协议/hooks：已安装（列出关键路径）
- config：main_branch=… · max_parallel=…
- 下一步：maw-task-list（指定主分支）→ maw-plan-push → `plan apply --yes`
```

## 反模式

- 未装 maw / 版本过旧就写蓝图  
- 在已有进行中任务的工程上盲目 `--upgrade` 覆盖 config  
- 假定主干永远是 `main`、不在任务清单里写 `main_branch`，导致 worktree 基线错误  
- 把示例 blueprint 直接 apply 当正式队列（应换真实剩余题）
