# Pi 节点与 Lead 操作说明

本文覆盖三件事：怎么把 Pi 运行时接成受管节点、怎么用 Lead 走「目标 → 计划 → 批准 → 入队」、
以及 Worker／Lead／诊断的用量在统计里怎么读。系统 Agent 的开关矩阵见
[系统 Agent 操作说明](system-agent.zh-CN.md)，这里只讲「把诊断后端换成 Pi 节点」这一段。

> 本文的「已验证 / 实验 / 未验证 / 不支持」只写证据能支撑的结论。机检证据在
> [Pi 端到端验收证据](../../verification/pi/acceptance.zh-CN.md)；真实供应商 API 未验项
> 也登记在那里（§4），不要按「全部能力已通过」对外表述。

## 1. 支持分级

| 能力 | 级别 | 说明与证据 |
| --- | --- | --- |
| Pi 运行时探测（`--probe` 自报协议与 SDK 版本） | 已验证 | `node src/main.mjs --probe`；节点登记后由看板探测循环或「测试环境」按钮触发 |
| Pi Worker 受管执行（macOS） | 实验 | 进程、journal、事件边界、终态与用量落库都有机检（PI-901）；**真实供应商响应未验** |
| Pi Lead 规划／批准入队／返工（macOS） | 实验 | 计划修订不可变、提案不改蓝图、返工需再批准；机检见 PI-901 |
| 系统 Agent 诊断改用 Pi（`diagnose` 模式） | 实验 | 只读运行、用量按诊断作业归属、超时／认证失败／非法结构有审计结论（PI-305） |
| 用量归属与覆盖率（统计页） | 实验 | 来源／节点／模型／用途四维归集，未知≠0，零分母不写 0%（PI-203） |
| 真实供应商 token 计数、429、断流、成本估算 | 未验证 | 需要授权凭据与真实限流现场；报表只报 token，不编单价 |
| 非 macOS 受管启动 | 不支持 | 受管进程控制只在 darwin 验证过；其它平台用人工领取 + 观测 |
| 网络工具、自动派发 | 默认关闭 | 不因安装 Pi 而自动打开；要开需显式配置并留审计 |

## 2. 安装与探测

前置：Node ≥ 22.19（Pi SDK 要求），仓库带 `runtimes/pi/package-lock.json`。

```sh
cd runtimes/pi && npm ci && cd -      # 离线复现同一版本树；不要用 ^ / ~ / latest
node runtimes/pi/src/main.mjs --probe # 应输出 {"ok":true,"protocol":{...},"sdk":"x.y.z",...}
```

启动前运行时会自检三项并在不符时**拒绝启动**（不回退猜兼容）：契约版本 `maw.pi.execution/v1`、
协议 `pi-stream-json/1`、SDK 精确版本。拒绝原因会写进 stderr 与节点 `last_probe_error`。

## 3. 登记节点并跑第一个 Run

```sh
# 1) 登记：argv 必须走数组（禁止 shell 拼接），入口指向本仓 runtimes/pi/src/main.mjs
maw node register --id pi-1 \
  --cmd '["'"$(command -v node)"'","'"$PWD"'/runtimes/pi/src/main.mjs"]'

# 2) 让探测跑一次：看板常驻循环会自动探测；也可在看板节点页点「测试环境」
maw board --serve
```

`[managed] limit` 必须 ≥ 1（显式 `--task --node` 不需要打开 `auto_enabled`）：

```toml
[managed]
auto_enabled = false   # 默认关闭自动派发；手动启动不需要打开它
limit = 1
```

```sh
# 3) 跑一张已领取的卡（显式指定任务与节点）
maw managed start --task T-1 --node pi-1
# 需要临时换模型时：--model <model-id>（只对本次执行生效）

maw managed status   # 看 run_id / operation_id / workspace / 是否已 detached
```

产物位置：`<global>/runtime/<project>/ops/<operation>/` 下有 `launch_spec.json`、`run_meta.json`、
`journal/`；任务卡与写盘范围来自领取时的冻结定义。Worker 只允许在 `write_scope` 内写盘，
越界写会被运行时拒绝并记 `tool_denied`（不会「先写了再 review」）。

## 4. Lead：目标 → 计划 → 批准 → 入队

前置：调用者必须是 `[identity] leads` 里的身份（或 `MAW_AGENT` 指定已授权身份）。
模型凭据只放环境变量（`PI_PROVIDER`／`PI_MODEL`／`PI_BASE_URL`／`PI_API_KEY`），
不进 argv、不进仓库文件、不进日志。

```sh
export MAW_AGENT=pi-lead-1          # 须在 [identity] leads 名单里
export MAW_PI_RUNTIME="$PWD/runtimes/pi/src/main.mjs"   # 缺省也指向仓内同一路径
export MAW_PI_NODE="$(command -v node)"

# 1) 规划：只落 proposed，不改蓝图
maw lead goal plan --goal gl_0123456789ab \
  --title "把需求拆成可交付卡" \
  --statement "现状：只有一段需求。目标：拆成可领取、可验收的卡。" \
  --acceptance "evidence: delivery|全部映射任务已交付" \
  --max-cards 4

# 2) 审查（只读）
maw lead goal show --goal gl_0123456789ab

# 3A) 人工审批：先批准、再单独入队
maw lead goal approve --goal gl_0123456789ab --rev 1 --policy manual
maw lead goal enqueue --goal gl_0123456789ab --rev 1

# 3B) 自动入队：批准与入队在同一事务里
maw lead goal approve --goal gl_0123456789ab --rev 1 \
  --policy auto_enqueue --auto-enqueue
```

返工（整体验收失败后加卡）：

```sh
maw lead goal plan --goal gl_0123456789ab --kind rework \
  --title "按失败证据返工" --statement "现状：AC-1 未通过。目标：修复并重新验收。" \
  --acceptance "evidence: delivery|全部映射任务已交付"
```

返工的三条硬约束（机检见 PI-901）：旧修订与蓝图在**提案阶段**不变；返工卡只有再次
`approve` 后才入队；已在执行的题不被改写。

## 5. 把系统 Agent 的诊断后端换成 Pi（可选）

```toml
[system_agent]
enabled = true
llm_node = "pi-1"          # 填已登记的 Pi 节点：诊断走 diagnose 模式，只读
diagnose_timeout_sec = 180 # 超时会留可审计结论，不会被当成「还在跑」
```

```sh
maw system status   # diagnose_backend=pi / diagnose.node_id=pi-1 / degraded 原因
```

解析顺序：显式 `llm_node`（Pi）→ 旧 `llm_command` → 规则降级（`escalate_human`）。
Pi 节点未就绪（未登记、协议证据缺失、入口不存在）时**不会偷换后端**：作业记升级人工，
原因写进决策与 `maw system status`；补齐探测后重跑即可。旧的 `llm_command` 配置照旧可用。

## 6. 统计口径：旧 CLI 与 Pi 怎么看

- **用途**：`usage_kind` 是封闭集合 `worker`／`lead`／`diagnose`；Lead 与诊断独立归属，
  不并进 Worker 的 Run 用量。
- **粒度**：Pi 给逐次调用（`call`），旧 CLI 往往只有整次运行汇总（`run`）。统计页的
  「用量归属」按行标出实际口径；两者同时出现标「逐次 + 运行级」，不合并成一次调用。
- **未知不等于 0**：来源没给 token 时字段是 `null`，计入轮次但不进 token 合计；
  `estimated` 必须带 `basis`；缓存字段独立，缺归一证据时标「缓存未归一」。
- **覆盖率写分母**：调用覆盖 = 有用量的调用 / 调用数，轮次覆盖 = 有用量的轮次 / 轮次；
  分母为 0 时写「无分母」，不写 0%。
- **费用**：未配置价目表时只报 token 与质量，不编单价（供应商账单保持独立证据）。

查看方式：

```sh
maw system health                     # 自愈/系统作业报表（JSON）
maw board --serve                     # 统计页 →「用量归属」：来源/节点/模型/用途切换
# 接口同源：/api/v1/analytics?dimension=source|node|model|purpose（可加 node_id/model/purpose 过滤）
maw stats report --turns 3 --duration 900   # 外部 Agent 上报自己的执行
```

## 7. 取消与恢复

- **取消**：运行态取消走命令接口 `POST /api/v1/runs/{run_id}/cancellations`（需写权限）。
  先记 `desired_action=stop`，等进程退出确认后才落 `cancelled`；没确认就保持 `unknown`
  并继续占用，不重试到把证据洗掉。
- **超时**：`timeout` 类失败带预算与停止证据；诊断侧还会留 journal 与 `pi-diagnose.json`。
- **恢复**：`unknown` 现场交核实流程（`run_reconcile`），按端口结论 release／keep／escalate；
  观测写库失败的原始记录留在 journal，可用同一去重身份补录（重复补录只记一笔）。
- 重跑一律新 Run、新代际；不要手改库里的阶段或占用来「让它继续」。

## 8. 排障

| 现场 | 先看哪里 | 处置 |
| --- | --- | --- |
| 节点探测失败 | 节点 meta 的 `last_probe_error`、`maw board --serve` 日志 | 修 Node 版本／入口路径；`--probe` 手动复现 |
| 启动被拒（版本／模式不符） | stderr、`ops/<op>/` 下的启动规格 | 对齐 `pinned_sdk_version`；不要降级成 Passthrough |
| 诊断一直升级人工 | `maw system status` 的 `diagnose.degraded` / `reason` | 补登记、补探测证据，或先移除 `llm_node` 回到规则层 |
| 用量「部分未知」 | 统计页归属面板的未知/估算标记；`usage_records.quality` | 补证据或换能报 token 的来源；不要填 0 |
| 作业失败但看不到原文 | 作业结果里的 `decision_raw_path`／`journal` 路径（0600） | 从审计目录取原文；脱敏后再贴到 issue |
| 写盘被拒 | journal 的 `tool_denied` 事件 | 检查任务 `write_scope`；越界不是靠 review 兜底 |

## 9. 安全边界

- 凭据只出现在运行时进程的**环境白名单**里；工具子进程 env 会剔除 `*_API_KEY`／`*_TOKEN`／`*_SECRET`，
  argv 与日志里不得出现密钥原文。本机令牌只允许 Authorization 头或 0600 文件。
- 启动与工具执行都用 argv 数组，禁止 `sh -c` 拼接；一次 Run 内授权固定，不热改。
- 诊断与 Lead 都是只读模式：不能改任务／Run 状态，不能领题或合入；越权请求记 `tool_denied`。
- 网络工具与自动派发默认关闭；打开属于显式运维决定，会进审计。
- 未验项（真实供应商响应、真实限流、真实取消时序、成本）不得当成已验证能力对外承诺。
