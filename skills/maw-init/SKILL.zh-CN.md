---
name: maw-init
description: >-
  将 git 工程登记进本机 maw（maw init / project add、仓外 config、doctor）。
  Use when the user asks to maw init、初始化 maw、接入 maw、project add、
  doctor 提示未接线、或首次把仓库挂进 maw 看板。
---

> 语言：[English](SKILL.md) | 简体中文

# maw 工程接入（仓外-only）

在**仓库根目录**（git root）操作。总览见 [maw](../maw/SKILL.md)。  
下一步通常：[maw-task-list](../maw-task-list/SKILL.md) 拆清单 → [maw-plan-push](../maw-plan-push/SKILL.md) 压入。

## 何时用

- 新仓库首次挂上本机看板 / 准备 `register`/`next`
- `maw doctor` 报未接线
- 用户说「初始化 maw」「接入看板」「project add」

**说明**：进仓执行 `maw register` / `maw next` / `maw board` 也会自动 `EnsureAttached`，多数情况不必单独 init。

## 前置

```bash
which maw && maw --version
git rev-parse --show-toplevel
```

非 git 仓库先 `git init`。全局状态在 `~/.maw/maw.db`（可用 `MAW_GLOBAL` 覆盖）。

## 工作流

```text
[1] 预检 → [2] maw init 或 project add（或直接 next）→ [3] 看仓外 config → [4] doctor → [5] 拆清单
```

### 1) 预检

```bash
maw doctor
```

升级仓外默认 config（会覆盖已有仓外 `config.toml`，先备份）：

```bash
maw init --upgrade
```

### 2) 接入

```bash
maw init
# 或只登记：maw project add
# 可选预览：maw init --dry-run
```

**会创建/登记（均在本机，不写工程树）**

| 产物 | 说明 |
|---|---|
| `~/.maw/maw.db` | 全局任务/Agent/工程库 |
| `~/.maw/projects/<id>/` | 仓外 home：config、blueprint、board、handoffs |
| `config.toml` | 主干、并行度、审批、identity |
| `blueprint.toml` | 示例蓝图（随后由 maw-plan-push 重写） |

**不会**：创建仓内 `.multiagent/`、注入 `AGENTS.md`/IDE rules、安装 git hooks、改 `.gitignore`。  
仓内历史接线文件若存在，**运行时不读**；需要旧配置时手工拷到仓外 home。

### 3) 查看仓外 config（占位即可）

```bash
# doctor / init 输出里的 home / project_id
ls ~/.maw/projects/<id>/config.toml
```

`main_branch` 为探测占位；正式主干在任务清单 / 蓝图 `[meta] main_branch`，由 `plan apply` 同步。

```toml
[project]
main_branch = "main"
merge_strategy = "squash"
max_parallel = 0
blueprint = "blueprint.toml"
```

### 4) 验收

```bash
maw doctor          # ok；不再检查 AGENTS.md / hooks
maw board           # 应出现本工程
```

### 5) 交棒

1. **maw-task-list**（文首写明主分支）
2. **maw-plan-push** → `plan apply --yes`
3. `maw register && maw next` → `maw next --claim`
4. 可选：`maw board --serve`

## 硬规则

1. 只在目标仓库根执行。
2. 配置权威在 `~/.maw/projects/<id>/`，不是仓内 `.multiagent/`。
3. init / project add 不 apply 业务蓝图。
4. 普通 init **不覆盖**已有仓外 config；`--upgrade` 会重写仓外 config 默认模板。
5. submit 后可自行 `maw merge --yes`；禁止直接 git merge/rebase/push 主干。

## 输出给用户（中文）

```markdown
**maw init / project add**
- 工程：`<repo>` · project_id=`…`
- 全局库：`~/.maw/maw.db`
- 仓外 home：`~/.maw/projects/<id>/`
- 下一步：maw-task-list → maw-plan-push → `plan apply --yes`
```

## 反模式

- 假定必须先写仓内 `.multiagent` / 协议段才能接入  
- 把仓内旧 config 当成仍生效（需手工拷到仓外）  
- 把示例 blueprint 直接 apply 当正式队列  
