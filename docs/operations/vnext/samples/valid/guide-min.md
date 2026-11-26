# 操作指南（最小正例）

<!-- ops-contract
automatic_default: off
managed_platforms: darwin
codebuddy_scope: manual_loop_only
-->

这份文件只用于校验器自测：结构齐全、口径正确，但不承载真实操作内容。

## 适用范围

单机、单用户。

## 运行方式

默认人工领取；受管执行需显式指定任务与节点。

## 平台与来源支持分级

| 能力 | 名称 | 级别 | 启用条件 | 依据 |
| --- | --- | --- | --- | --- |
| `source.manual` | 人工领取闭环 | 已验证 | 无条件 | 端到端已验收 |
| `dispatch.auto` | 自动匹配与派发 | 实验 | 仅 darwin；需 `auto_enabled = true` | 仅 macOS 实测 |

## 日常操作

`maw next` → `maw submit` → `maw merge --yes`。

## 首次接入

`maw init` → `maw register` → `maw next`。

## 排障

看原因码，不要猜。

## 安全边界

中枢只监听 `127.0.0.1`。
