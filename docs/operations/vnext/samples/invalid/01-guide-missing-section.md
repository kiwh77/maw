# 操作指南（反例：缺章节）

预期命中：`OPS001`。

<!-- ops-contract
automatic_default: off
managed_platforms: darwin
codebuddy_scope: manual_loop_only
-->

## 适用范围

单机、单用户。

## 运行方式

默认人工领取。

## 平台与来源支持分级

| 能力 | 名称 | 级别 | 启用条件 | 依据 |
| --- | --- | --- | --- | --- |
| `source.manual` | 人工领取闭环 | 已验证 | 无条件 | 端到端已验收 |
| `dispatch.auto` | 自动匹配与派发 | 实验 | 仅 darwin；需 `auto_enabled = true` | 仅 macOS 实测 |

## 日常操作

`maw next` → `maw submit`。

## 首次接入

`maw init`。

## 安全边界

中枢只监听 `127.0.0.1`。

（少了「排障」一节：读者找不到症状到处置的映射。）
