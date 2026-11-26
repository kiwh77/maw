# Security Policy

> 语言：[English](SECURITY.md) | 简体中文

## 支持的版本

请使用最新发布的 `maw`（或从 `main` 构建）。旧版本通常不再单独修安全问题。

## 信任边界

`maw` 是**本机、单用户**工具：中枢监听 `127.0.0.1`，写操作要求本地鉴权。不要把中枢暴露到 `0.0.0.0`、
公网接口或共享／挂载盘——状态库里是任务内容、Agent 名与事件流，而且它没有多用户授权模型。

静态导出（`file://` 或导出目录）是**只读**的：`/api/runtime` 会报 `static_export_managed: false`，
该场景下写入类控件应当禁用或降级。

## 受管执行

`maw managed start` 以你的操作系统用户身份起子进程，权限与你手工执行相同。由此有两条边界：

- 只在 macOS 上被声称支持——原生启动／停止／回收只在那台平台被实际观测过；Linux 与 Windows 能编译，
  但不支持受管执行。
- 自动派发（`[managed] auto_enabled`）**默认关闭**。打开它等于让中枢自己挑题、自己起进程，
  请把它当作「给这个工程提权」而不是一个顺手开关。

## 数据完整性

- **升级前必须备份。** 升级走一致的 `VACUUM INTO` 快照并做校验；用 `cp` 拷贝活动中的 `maw.db`
  会静默丢掉尚未 checkpoint 的 WAL 帧。
- **不要用 `sqlite3` 直接写库。** 直写绕过写者闸门与引用一致性检查；旧二进制把新库「降级写」成功，
  结果是一份看起来正常、实际已经不一致的数据。
- 备份文件与库内容同等敏感。不要提交进仓库，也不要放到共享存储。

升级步骤、故障恢复与 `unknown` 解除规则见
[docs/operations/vnext/upgrade-notes.zh-CN.md](docs/operations/vnext/upgrade-notes.zh-CN.md)；
支持范围与平台边界见 [docs/operations/vnext/operations-guide.zh-CN.md](docs/operations/vnext/operations-guide.zh-CN.md)。

## 报告漏洞

请**不要**在公开 Issue 中披露可利用细节。

通过 GitHub Security Advisories（仓库 Security 页）私密报告，或联系维护者。请包含：

- 影响版本 / 提交
- 复现步骤或 PoC（私密渠道）
- 预期影响范围

我们会确认收到并评估修复优先级。
