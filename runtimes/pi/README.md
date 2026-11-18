# runtimes/pi

Pi SDK 执行器：Worker、Lead、诊断三种模式共用一个执行器与同一套输出协议。
协议与授权口径由 [`docs/contracts/vnext/execution/pi-execution-contract-v1.md`](../../docs/contracts/vnext/execution/pi-execution-contract-v1.md)
（`maw.pi.execution/v1`）冻结；本目录只实现运行时侧。

## 依赖与版本

| 项 | 值 |
| --- | --- |
| Pi SDK | `@earendil-works/pi-coding-agent` / `pi-agent-core` / `pi-ai`，精确锁定 `1.5.2` |
| Node | `>= 22.19.0`（SDK 要求；本机 24.x 已验证） |
| 锁文件 | `package-lock.json` 随仓库提交，`npm ci` 可离线复现同一版本树 |

升级 SDK：改 `package.json` 里的精确版本 → 重新生成锁文件 → 重跑能力核对与测试。
**不使用** `^` / `~` / `latest`。

## 结构

| 文件 | 作用 |
| --- | --- |
| `src/protocol.mjs` | 事件信封、用量归一（`null ≠ 0`、缓存独立、total 重算）、错误分类、终态映射 |
| `src/policy.mjs` | 三模式授权矩阵、写盘范围判定、路径归一、凭据剔除与脱敏 |
| `src/executor.mjs` | 工具装配 + SDK `Agent` 循环 + 事件翻译；一次 Run 一个执行器 |
| `src/main.mjs` | 单次执行入口：stdin 一行 JSON 配置，stdout NDJSON 事件 |
| `src/index.mjs` | 对 maw 暴露的入口 |

## 三种模式

| 模式 | 工具 | 写盘 | 说明 |
| --- | --- | --- | --- |
| `worker` | `read`、`write`、`edit`（节点配置允许时加 `bash`） | 仅 `writeScope` | 执行一张已领取的卡 |
| `lead` | `read` | 无 | 只产出计划卡，不能落地 |
| `diagnose` | `read` | 无 | 只产出决策，不能落地 |

授权在**工具边界**执行：`write`/`edit` 的 operations 会先判定模式与 `writeScope`，
越界直接抛 `tool_denied`；`bash` 子进程环境经 `scrubEnv` 剔除凭据，默认不启用。

## 配置

`src/main.mjs` 从 stdin 读一行 JSON（camelCase 与 snake_case 都接受）：

```jsonc
{
  "mode": "worker",
  "cwd": "/abs/path/.worktrees/agent-1",
  "run_id": "run_a001",          // 必填
  "generation": 1,                // 必填，>=1
  "write_scope": ["internal/goal/**"],
  "read_scope": ["docs/**"],      // lead/diagnose 生效
  "allow_bash": false,
  "prompt": "实现 PI-301 的持久化层"
}
```

模型凭据只从进程环境读取（宿主 `EnvAllowlist`），不进 argv、不进输出。

```sh
# 版本与协议探测（无需密钥）
node src/main.mjs --probe

# 单次执行：事件逐行打印
echo '{"mode":"worker","cwd":"'$PWD'","run_id":"run_1","generation":1,"prompt":"hi"}' | node src/main.mjs
```

退出码：`0` 完成；`1` 失败；`2` 配置被拒绝；`65` 取消。

## 事件

每条事件都带 `contract`、`event_id`、`run_id`、`generation`、`occurred_at`、`received_at`：

| type | 何时 | 关键 payload |
| --- | --- | --- |
| `version_report` | 启动 | `sdk_version`、`protocol`、`mode`、`tools` |
| `session_started` / `session_ended` | 首末 | `mode`、`cwd`、`stop_reason`、`aborted` |
| `usage` | 每次模型调用 | `scope=call`、四个 token 分量、`quality`、`basis` |
| `tool_call` / `tool_result` | 工具执行 | 名称、参数摘要（已脱敏）、`is_error`、`duration_ms` |
| `error` | 模型/环境失败 | `category`（封闭集合）、`message`（脱敏）、`retryable` |
| `tool_denied` | 授权拒绝 | `reason`、`detail` |
| `result` | 唯一终态 | `ok`、`outcome`、`reason`、`evidence[]` |

## 测试

```sh
npm ci
npm test
```

测试用注入的 `streamFn` 与临时目录，**不需要模型凭据、不访问网络**，覆盖：
成功终态与逐次用量、认证失败分类、取消、无密钥观测、lead 只读与越界写拒绝、
配置错误在启动即拒绝。

## 本目录不做的事

- 不登记节点、不接 Runner、不打包发布（`PI-102`／`PI-103`）。
- 不写数据库、不改任务或 Run 状态（`PI-201` 起）。
- 不判断交付是否完成（`maw.delivery.manifest/v1`）。
