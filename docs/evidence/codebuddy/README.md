# CodeBuddy 版本化协议证据（VN-301）

**一句话结论**：在 macOS 上用本机 CodeBuddy CLI `2.173.0` 实测到「单回合可完成、事件流可解析、结果记录可判定」，
同时实测到 **退出码 0 也可能是失败**（认证缺失）——所以结束判定只能看 `result.subtype` + `is_error` + 正文，不能看退出码。
取消、恢复、工具调用、用量聚合口径**未观察**，一律保持 unknown，不做推算。

采集时间：2026-10-23；平台：darwin；CLI：`codebuddy 2.173.0`（`--version` 实测，样本 S01）。
验收命令：`python3 -m unittest discover -s docs/evidence/codebuddy -p 'test_*.py'`（41 条，全绿）。

---

## 一、实测到了什么

| 样本 | 动作 | 实测结果（原文数字） |
| --- | --- | --- |
| S01 | `--version` | 退出 0，stdout 单行 `2.173.0`；不登录、不发模型任务 |
| S02 | `--help` | 退出 0，13,452 字节用法清单；启动参数以此为准 |
| S03 | `-p … --output-format stream-json --include-partial-messages` | 退出 0；23 条记录 = init 1 + status 1 + stream_event 18 + assistant 2 + result 1；`result.result="MAW-OK"` |
| S04 | `-p … --output-format json` | 退出 0；stdout 是**单个 JSON 数组**（5 条会话记录），字段集与 S03 不同 |
| S05 | `-p …`（默认 text） | 退出 0；stdout 仅 `MAW-OK\n`（7 字节），无结构化结束证据 |
| S06 | 端口被占用时启动 | 退出 **1**，stdout 空，stderr `Error: listen EADDRINUSE … (use --port …)` |
| S07 | `--output-format bogus` | **未拒绝**：退出 0，输出与 text 模式逐字节相同（7 字节 `MAW-OK`） |
| S08 | 隔离空 `HOME` 下同一调用 | 退出 **0**，但 `result.subtype=error_during_execution`、`is_error=true`、`errors=["Authentication required…"]` |

从样本直接得到的协议事实（每条都有测试钉住）：

- **记录顺序固定**：`system/init` → `system/status` → `stream_event*` → `assistant*` → `result`，`result` 恒为最后一条。
- **同回合出现 2 条 `assistant`**（thinking 一条、text 一条），按 `_messageId` 去重，简单累加会重复计数。
- **`stream_event` 是 SSE 原样透传**（`message_start` / `content_block_*` / `message_delta` / `message_stop`），不是归一化事件。
- **失败时错误文本会写进 `assistant` 正文**（S08），直接取正文当交付就是假 done。
- **`usage.input_tokens` 已包含缓存分解**：23,945 = 23,433 + 512；23,941 = 21,125 + 2,816。把 `input_tokens` 与 `cache_*` 相加会重复计算。
- **`num_turns` 不是用户轮次**：单轮回复为 3，认证失败为 2。
- **`total_cost_usd` 成功与失败都是 0**，0 不代表免费，口径未验证。
- **`modelUsage` 可为 `null`**（S04 json 模式），解析必须允许 null。

## 二、没有观察到的（保持 unknown / unobserved）

| 能力 | 状态 | 原因 |
| --- | --- | --- |
| 取消 / 优雅停止 | unknown | 本题未向运行中进程发信号（避免留孤儿进程），无任何实测 |
| `-r/--resume`、`-c/--continue` | unknown | 未执行，恢复语义与会话可用性均未验证 |
| 用量聚合口径 | unknown | `assistant` 记录也带 usage（thinking 条为 0、text 条为全量），多记录聚合未验证 |
| 稳定错误码 | unobserved | 只有自由文本错误，样本中没有错误码字段 |
| 工具调用 / 权限拒绝 | unobserved | 固定提示不需要工具，未触发 tool_use 路径 |
| json/text 模式下的回合内错误位置 | unobserved | 只观察到启动期错误走 stderr |

`capability-matrix.json` 里这些行 `samples` 为空、状态非 observed；测试会拦住「未验证却挂样本」的写法。

## 三、合成反例（明确标为合成，不得当实测引用）

| 样本 | 缺陷 | 用途 |
| --- | --- | --- |
| X01 | 事件流中间一行被截断（非法 JSON） | 解析器必须保留证据、不得推断文本 |
| X02 | 缺 `result` 记录 | 运行只能判「不完整／待核实」，不得判 done |
| X03 | `subtype=success` 但正文为空、用量全 0 | 最容易被误判成成功的形态 |
| X04 | 未知事件类型 + 未知字段 + 未知版本 | 未知记录不得映射为状态迁移 |

X03/X04 含未实测取值，已在 `meta.assumed_values` 逐条声明；X01/X02 只做结构性破坏，不引入新值。

## 四、脱敏

样本落盘前过 14 条规则（`manifest.json.redaction_rules`）：家目录、临时路径、UUID/32 位请求号、traceparent/baggage、
凭据形态、邮箱、工具名、MCP 名、`apiKeySource`、回环端口、CLI 绝对入口、**CLI 注入的长上下文**（S04 的 user 记录里
原本有 13,965 字符记忆提示，已替换为 `<CLI_INJECTED_CONTEXT>`）、消息标识。

不记录：账号、令牌、项目正文、真实工作目录。`collect/collect_probe.py verify-redaction` 与 `RedactionTest` 双向扫描。

## 五、怎么复现

```sh
python3 collect/collect_probe.py collect --dry-run   # 先看要跑什么
python3 collect/collect_probe.py collect             # 重采并覆盖 samples/ + manifest 哈希
python3 collect/make_synthetic.py                    # 重建合成反例
python3 -m unittest discover -s docs/evidence/codebuddy -p 'test_*.py'
```

采集只继承白名单环境（`PATH/HOME/LANG/LC_ALL/TERM/TMPDIR/SHELL`）。**这一点有代价也有收获**：
继承宿主应用的私有环境（含指向另一份配置目录的 `CODEBUDDY_CONFIG_DIR`）时，同一调用曾返回认证缺失；
该次运行未留存脱敏样本，故不计入能力证据，只提示 VN-303：启动时固定环境白名单并显式覆盖。
S08 证明登录态随 `HOME` 解析（空 `HOME` 即未登录），不来自配置目录。

## 六、给下游的直接结论

- **VN-303（探测与启动规格）**：`--version`/`--help` 可安全探测（不登录、不发任务）；启动**必须显式传 `--port`**
  （默认端口会被本机其他监听占用，S06 直接退出 1）；不能依赖 CLI 校验枚举（S07）；参数按数组传，环境用白名单。
- **VN-304（事件与结束判定）**：以 `result` 记录为准，`subtype=success` 且 `is_error=false` 且正文非空才算完成；
  退出码 0 单独不成立（S08）；缺 `result` 判不完整（X02）；未知事件只留证据（X04）；`assistant` 正文可能是错误文本（S08）。
- **VN-308（轮次与用量）**：`usage.input_tokens` 与 `cache_*` 不可相加；`num_turns` 与 `total_cost_usd` 语义未定，
  不得直接展示为「轮次/费用」；聚合口径未验证前，只允许展示单次 `result.usage` 原值并标注来源。

## 七、边界

本题只做证据采集，不改产品代码、不装工具、不切账号、不改项目；不含任何「按能力推算」的结论。
版本、入口或平台变化后，本目录的 observed 结论自动失效，需重采（见 `capability-matrix.json.version_binding`）。
