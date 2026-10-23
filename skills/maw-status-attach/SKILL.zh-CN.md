---
name: maw-status-attach
description: >-
  非 maw next 领取的任务一键接入 maw：注册 Agent、向本机中枢及时上报状态/心跳（maw status report），
  让看板可见旁路工作。Use when the user asks to 接入 maw、旁路任务、非领取任务、status report、
  心跳上报、外部进度、adhoc/用户指定任务挂看板，或不走 maw-task-pull 但仍要告之 maw。
---

> 语言：[English](SKILL.md) | 简体中文

# maw 旁路接入（status-attach）

系列总览：[maw](../maw/SKILL.md)。  
**本技能 ≠ 领题**：不跑 `maw next`、不占用编排 worktree 派发。只把当前 Agent 的存在感与进度注入 `~/.maw/maw.db` / 本机中枢，供 `maw board --serve` 与桌面端看见。

正式领题请用 **[maw-task-pull](../maw-task-pull/SKILL.md)**。工程未 init → **[maw-init](../maw-init/SKILL.md)**。

需要 **maw ≥ 0.4.0**（`maw status report`）。

## 何时用

| 场景 | 用本技能 | 用 maw-task-pull |
|---|---|---|
| 用户直接指派改某文件/修 bug，无 ready 题或不想领队列 | ✅ | |
| 探索、调研、文档、本地实验，仍要看板显示「谁在干什么」 | ✅ | |
| 已有看板题 ID，但暂不走 next/worktree（只挂心跳；可选 `--task`） | ✅ | 优先 next |
| 从 ready 队列领题并进 `.worktrees/{agent}` | | ✅ |
| 要 `submit` / `merge` 合入题分支 | | ✅（编排链路） |

**铁律：旁路接入必须及时把状态告之 maw。** 沉默 = 看板以为你 idle/离线，调度会误判。

## 一键接入（开工立刻做）

在目标工程根（已 `maw init`）执行：

```bash
# 1) 身份：{应用}-{工程slug}-{序号}；未指定则自动分配
maw register --json
export MAW_AGENT=<返回的 agent>          # 本会话固定，勿多人共用
# 应用段可用 MAW_APP=cursor|claude|… 覆盖

# 2) 立刻上报「开始工作」（必做）
maw status report \
  --agent "$MAW_AGENT" \
  --client "${MAW_APP:-cursor}" \
  --status working \
  -m "旁路开工：<一句话目标>" \
  --phase start \
  --pct 0
# 若已知关联看板题：再加 --task T-xxx
```

成功 JSON 含 `ok` / `event_type`（有 `-m` 时为 `external_progress`）/ `next`。  
可选确认：`maw agents --json`；看板：`maw board --serve`（默认 `http://127.0.0.1:7420/`）。

无中枢进程时，CLI 仍写入全局库；起服务后 SSE/轮询即可看到历史事件。

## 及时上报（执行中强制节奏）

旁路任务**没有** `maw progress` 绑定的编排题时，一律用 `maw status report`。有编排题且已 `next` 时优先 `maw progress`；本技能场景默认走 report。

| 时机 | 命令要点 |
|---|---|
| 开工 / 换目标 | `--status working` + `-m` + `--phase start` |
| 每完成一个可描述里程碑（约 15–30min 或阶段性结果） | `-m "…"` + 更新 `--phase` / `--pct` |
| 阻塞、等人、等依赖 | `-m "阻塞：…"` + `--phase blocked`（仍 `--status working` 除非彻底停） |
| 本会话结束仍续做 | `-m "暂停：…"` + `--status idle` |
| 全部做完 | `-m "完成：…"` + `--pct 100` + `--status idle`，再视情况 `maw unregister` |

模板：

```bash
maw status report \
  --agent "$MAW_AGENT" \
  --client "${MAW_APP:-cursor}" \
  --status working \
  --phase <explore|code|verify|blocked|done> \
  --pct <0-100> \
  -m "<对人可读的一句进展>"
# 可选：--task T-xxx   --project <工程ID>   -C <工程路径>
```

HTTP 等价（中枢已起、仅 loopback）：

```bash
curl -sX POST http://127.0.0.1:7420/api/status \
  -H 'Content-Type: application/json' \
  -d "{\"actor\":\"$MAW_AGENT\",\"message\":\"…\",\"phase\":\"code\",\"pct\":40,\"client\":\"cursor\",\"status\":\"working\"}"
```

**最少义务**

1. 开工第一条 `working` report（一键接入）  
2. 每个用户可见里程碑至少一条 `-m`  
3. 结束或换人：`idle` 或 `unregister`，禁止假 working 挂尸  

## 与编排命令的边界

- **不要**对旁路工作假跑 `maw next` / `maw submit` / `maw merge`（除非用户改口走正式领题）。  
- **不要**改他人 `.worktrees/*` 或抢已 claimed 题的 write_scope。  
- 旁路改代码：在用户指定目录改；若工程有协议段，仍尊重主干保护与 hooks。  
- 若工作后来变成看板正式题：停旁路 → [maw-task-pull](../maw-task-pull/SKILL.md) 领题 → 之后用 `maw progress` / `submit`。  
- 可选 `--task T-xxx`：仅关联心跳/owner 提示，**不会**把题状态改成 in_progress；真正领题仍须 `maw next`。

## 收工

```bash
maw status report --agent "$MAW_AGENT" --status idle -m "旁路收工：<摘要>" --phase done --pct 100
# 本会话不再做该工程：
maw unregister
```

换人 / 中断：先 report 当前进度与 `idle`，再交接上下文；勿留下 `working` 空壳。

## 失败对照

| 现象 | 处理 |
|---|---|
| 工程未 init / 无法解析工程 | maw-init；或 `--project` / `-C` |
| Agent 名冲突 | 换规范名或让 `register` 自动分配 |
| 看板看不到 | 确认写入成功；`maw serve` / `board --serve`；看 `maw agents` |
| 误用了 next | 若已领题，改走 task-pull 协议；勿混用两套收工 |

## 输出给用户（中文）

```markdown
**已旁路接入 maw**
- Agent：`cursor-…-1` · 工程：`…`
- 模式：status-attach（未 maw next）
- 首报：working / phase=start — …
- 约定：里程碑与收工继续 `maw status report`；不做 submit/merge
- 看板：`maw board --serve`
```
