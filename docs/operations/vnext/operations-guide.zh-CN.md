# maw vNext 操作指南

> 语言：简体中文（本目录暂无英文版）｜适用版本：v0.4.x（vNext 视图与受管执行接入后的版本）

<!-- ops-contract
automatic_default: off
managed_platforms: darwin
codebuddy_scope: manual_loop_only
-->

**结论：默认只跑人工闭环——Agent 自己领取、自己提交、自己合入。受管执行与自动派发都必须显式打开，
且只在 macOS 上被声称支持；「某个 CLI 能跑起来」不等于它的全部能力都已通过验收。**

本文与本目录的 [升级与迁移说明](upgrade-notes.zh-CN.md) 一起构成 vNext 的运维正文；
平台支持范围的唯一出处是
[平台支持矩阵与发布检查](../verification/vnext/release/platform-matrix.zh-CN.md)，
本文只引用不复述，避免两处说法打架。

## 适用范围

- 单机、单用户：中枢监听 `127.0.0.1`，库是本地 SQLite（`~/.maw/maw.db`，可用 `MAW_GLOBAL` 覆盖）。
- 仓库内只放 `.multiagent/`（`config.toml` / `blueprint.toml` / hooks），不放库。
- 多机、共享库、远程中枢**不在**当前支持范围；需要时请先当作新需求评审，不要用挂载盘或多副本硬拼。

## 运行方式

三种方式共用同一套任务占用规则（写盘范围、目录占用、合入串行），差别只在「谁起进程」。

| 方式 | 谁起进程 | 默认状态 | 前置条件 |
| --- | --- | --- | --- |
| 人工领取（manual） | 你自己所在的 Agent 客户端 | **默认可用** | 无 |
| 受管单次执行 | maw 中枢 | 可用但需显式指定任务与节点 | 平台为 macOS；`maw managed status` 报 `platform_supported: true` |
| 自动匹配派发 | maw 中枢循环 | **默认关闭** | `[managed] auto_enabled = true`、`limit >= 1`、macOS、扩展未被关闭 |

自动派发之所以默认关闭，不是还没做完，而是「让它自己挑题自己起进程」这件事的风险与收益不对等：
先让人工闭环跑顺、把证据攒够，再决定是否放开。配置写在仓库的 `.multiagent/config.toml`：

```toml
[managed]
config_version = 1
auto_enabled = false   # 默认就是 false，不要为了「试试看」改成 true
limit = 1              # 受管执行的全局硬上限；不会借用 max_parallel
```

`max_parallel` 仍然是**展示参考**，不参与受管执行的容量计算；两者语义不同，不要互换。

## 平台与来源支持分级

三级划分的依据是**证据**，不是意图：

| 级别 | 含义 | 运行表现 |
| --- | --- | --- |
| 已验证（verified） | 有端到端实测记录，含失败路径 | 正常启用 |
| 实验（experimental） | 有接口与单测，缺端到端证据 | 需满足前置条件，且 `MAW_EXTENDED` 未被关闭（`off`／`0`／`false`） |
| 不支持（unsupported） | 无证据 | 永不启用，配置写了也按关闭处理 |

### 来源与能力

下表与 `internal/app/runtime.go` 的扩展注册表同源，机器校验会双向比对（多一行、少一行、级别写错都会报）。

| 能力 | 名称 | 级别 | 启用条件 | 依据 |
| --- | --- | --- | --- | --- |
| `source.manual` | 人工领取闭环 | 已验证 | 无条件 | 人工领取／提交／合入全链路已验收 |
| `source.codebuddy` | CodeBuddy 来源 | 已验证 | 无条件 | 首个 CodeBuddy 闭环已验收 |
| `observability.api` | 只读观测与提醒 API | 已验证 | 无条件 | `internal/httpapi/observability` 单测 + 接线 |
| `source.zcode` | ZCode 来源 | 实验 | 需 Git 工程；扩展未关闭 | `docs/evidence/zcode/capability-matrix.json`（模型调用仍 unobserved） |
| `source.trae` | Trae 来源 | 实验 | 需 Git 工程；扩展未关闭 | `internal/adapters/trae`（仅接口与单测） |
| `source.workbuddy` | WorkBuddy 来源 | 实验 | 需 Git 工程；扩展未关闭 | `internal/adapters/workbuddy`（仅接口与单测） |
| `dispatch.auto` | 自动匹配与派发 | 实验 | 仅 darwin；需 Git 工程；`auto_enabled = true` | 受管执行仅 macOS 实测 |
| `workspace.nongit` | 非 Git 工作区 | 实验 | `auto_enabled = true` | `internal/workspace/nongit`（无远端基线，不能自动合入） |
| `dispatch.model_invoke` | 受管模型调用 | 不支持 | 永不开启 | 各来源适配器均报 unobserved，无实测证据 |

读这张表时注意两件事：

1. **`source.codebuddy` 是已验证，`dispatch.model_invoke` 是不支持。** CodeBuddy CLI 能在本机能跑通、
   能完成人工闭环，只证明前者；「maw 替你调模型并判定结果」这条链路还没有证据，所以按不支持处理。
   不要把「能运行」写成「全部能力通过」——那两句话之间差着一次端到端实测。
2. **实验级不等于能用。** 实验级的含义是「有接口、有单测，缺端到端证据」，它默认还要满足前置条件才会开；
   前置条件不满足时它会明确报原因码（见下节），不要靠猜。

### 平台

| 平台 | 构建 | 受管执行 | 人工接入 |
| --- | --- | --- | --- |
| macOS | 交叉编译 + 原生构建 | 支持（唯一有原生证据） | 支持 |
| Linux | 交叉编译 | 不支持 | 支持 |
| Windows | 交叉编译 | 不支持 | 支持 |

六个发布组合（三个系统 × amd64/arm64）的具体行、以及「交叉编译不算原生证据」的边界说明见
[平台支持矩阵](../verification/vnext/release/platform-matrix.zh-CN.md)。

## 看当前状态

不要靠文档猜「我这台机器现在开着什么」，问运行时：

```bash
maw managed status --json
# {
#   "auto_enabled": false, "config_version": 1, "managed_execution_limit": 1,
#   "platform": "darwin", "platform_supported": true,
#   "state_dir": "~/.maw/runtime/<project_id>"
# }

maw doctor --json      # 库路径、协议版本、issues
```

看板与 API 侧：

- `GET /api/runtime` 的 `auto_execution` 段：受管开关（`enabled`）、硬上限（`managed_execution_limit`）、
  配置版本（`config_version`）、平台是否支持（`platform_supported`），以及扩展开关 `extended_enabled`。
- 同一段的 `extensions` 数组：每个能力的 `id` / `name` / `level` / `enabled` / `reason` / `evidence`。
  其中 `level` 的取值是 `verified` / `experimental` / `unsupported`，依次对应上表的已验证／实验／不支持。
- 同一响应里的 `static_export_managed` 恒为 `false`、`managed_via` 为 `cli_or_api_v1`——静态页面不具备受管写入口。
- 设置页（看板 → 设置）展示同一份数据；静态导出页面是**只读**的，只能看不能改。

```bash
curl -s http://127.0.0.1:7420/api/runtime | jq '.auto_execution.extensions'
```

`enabled: false` 时一定带一个原因码，含义固定：

| 原因码 | 意思 | 怎么办 |
| --- | --- | --- |
| `auto_disabled` | `auto_enabled` 为 false | 确认要放开再改配置 |
| `platform_unsupported` | 当前平台不在白名单 | 换 macOS，或继续用人工接入 |
| `workspace_not_git` | 工程没有 `.git` | 该能力需要 Git 基线 |
| `extended_disabled` | `MAW_EXTENDED` 关了实验能力 | 需要实验能力时去掉该环境变量 |
| `no_evidence` | 级别是不支持 | 不要尝试打开，等有证据后改注册表 |

## 日常操作

```bash
cd /path/to/repo
maw init                       # 首次：注册工程并写入协议
maw plan apply .multiagent/blueprint.toml --yes

export MAW_AGENT=<你的 Agent 名>
maw register                   # 或直接用 maw next 自动注册
maw next                       # 领题，拿到 worktree 路径与 write_scope
# 在打印出的 worktree 里改 write_scope 内的文件
maw progress -m "完成一半"      # 心跳 + 进度；这不是可选项
git commit -am "..."
maw submit                     # 范围检查 → 门禁 → merging
maw merge --yes                # 串行合入；保留 worktree，删题分支
maw next                       # 还有 ready 就继续
```

四条容易踩的规矩：

1. **领到题就发心跳，之后每个里程碑再发一次。** maw 按 Agent 心跳判活，不按 `locks.ttl_sec`。
   埋头写十几分钟不打心跳，题会被别人重新领走，之后 `maw submit` 会报 `task ... is owned by <other>`。
2. **只能改 `write_scope` 里的文件。** 越界会在提交时被拦；需要改别的目录就新开一张卡，不要顺手改。
3. **提交前先确认分支不落后主干**（`git rev-list --count HEAD..main` 为 0），否则合入会把别人的改动退回。
4. **做不完就用 `maw block -m "..."` 交接**：写清分支 tip、已完成部分、接手命令。别把状态停在半路。

看板与旁路上报：

```bash
maw serve                      # 默认 http://127.0.0.1:7420/
maw status report -m "coding" --phase code --pct 40
```

## 首次接入

1. 装二进制：Release 页下载对应组合，校验 `checksums.txt`，放进 `PATH`；或 `./install.sh` 从源码装。
2. 确认能跑：`maw --version`、`maw doctor --json` 的 `issues` 为空。
3. 注册工程：仓库里 `maw init`；老仓库用 `maw init --upgrade`（会先备份，见升级说明）。
4. 写蓝图并压入：`maw plan apply .multiagent/blueprint.toml --yes`；`maw plan check` 只校验不落库。
5. 注册 Agent 并领第一题：`maw register` → `maw next`。
6. 想用受管执行：先 `maw managed status` 看 `platform_supported`；为 true 时
   `maw managed start --task <ID> --node <node_id>`。不要先开自动派发来「顺便验证」受管能力。

## 排障

| 症状 | 通常原因 | 处理 |
| --- | --- | --- |
| `maw next` 说没有可领的题，但看板上明明有 | 你的 Agent 槽位是 offline，maw 不回收已下线的 Agent | 在原 worktree 里 `maw register` 复活槽位 |
| `maw submit` 报一堆无关文件的 out-of-scope | 本地 `main` 领先 `origin/main`，diff 基线错位 | 对齐跟踪引用后再提交；根因是把本地主干推到远端或改用本地镜像仓，需要拍板 |
| `maw submit` 报 `is owned by <other>` | 心跳断过，题被别人重新领走 | 别再硬提交；把分支 tip 写进 `maw block -m` 交接，或等对方放回 |
| 状态永久卡在 `merging` | 并发 `maw merge` 把分支一起合掉又删了分支 | 先用 `git ls-tree -r --name-only main \| grep <你的文件>` 确认内容在主干，再重建同名分支补一条真实提交后重新 merge |
| `maw merge` 说主工作区脏 | 另一个 Agent 正在主工作区干活，或蓝图文档长期未提交 | 先看文件 mtime：几秒内还在变就别 stash（会把对方现场抽走），等它提交 |
| 门禁 `go test ./...` 红了且报 `operation not permitted` | 沙箱拦了 `/bin/ps`，不是代码失败 | 换一个能起进程的终端跑门禁；别反复 submit 硬撞，`attempts` 会涨 |
| 看板空白/样式错乱 | 前端 bundle 未重建，页面脚本与视图版本不匹配 | `npm --prefix internal/board/web run build` 重建 `assets/dist/board-web.js` |

## 安全边界

- 中枢默认只听 `127.0.0.1`，且写操作要求本地鉴权；不要把它暴露到公网或 `0.0.0.0`。
- 静态导出（`file://` 或导出目录）是**只读**的：`/api/runtime` 会报 `static_export_managed: false`，
  页面上的写入类控件应当禁用或降级。
- 库里有任务内容、Agent 名与事件流；备份文件按敏感数据处理，不要提交进仓库或上传。
- 需要直接动库时先读 [升级与迁移说明](upgrade-notes.zh-CN.md)：不要用 `sqlite3` 直接改线上库。
