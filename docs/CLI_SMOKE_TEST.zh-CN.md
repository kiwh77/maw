# 本机 CLI 启动与最小调用实测

日期：2026-10-23。范围：已安装应用的随包入口；未安装独立 CLI，未修改项目代码或手动更改账号／模型配置。

后续更新：同日用户授权安装 CodeBuddy 国内站 CLI。已按官方 npm 安装方式完成独立安装，版本 **2.173.0**；前文“未安装独立 CLI”描述的是首轮检查。最新结果见文末。

最新用户确认：用户随后手动测试 CodeBuddy 命令行，确认可以运行。后续架构规划据此采用“CodeBuddy CLI 可调用”的前提；本文此前认证失败输出保留为历史测试证据，不再表示当前被认证阻塞。本轮未复测，也未将该确认扩大解释为全部自动化能力已验收。

## 结果

三个 AI CLI 都已成功启动并输出帮助信息，但本次没有任何一个完成固定文本的模型回复。CodeBuddy CN 和 Trae CN 随包发现的入口是 IDE 启动与聊天入口，尚未验证为独立后台 AI 执行器。

| 应用 | 已执行检查 | 最小调用结果 |
| --- | --- | --- |
| WorkBuddy 5.8.3 | 内置 CodeBuddy CLI 2.161.0：版本、帮助均退出 0；支持 print、JSON、stream-json、ACP 等选项 | 正常联网重试约 3.0 秒后返回 `Authentication required. Please use /login command to sign in to your account`；无回复 |
| WorkBuddy AI 5.8.4 | 内置 CodeBuddy CLI 2.161.0：版本、帮助均退出 0；支持相似入口，但展示的模型列表与国内包不同 | 正常联网重试约 3.6 秒后返回同样的认证要求；无回复 |
| ZCode 3.16.2 | 内置 CLI 0.17.0：版本、帮助、`doctor --json` 均退出 0 | 默认启动先缺内置 provider 配置路径；用临时环境变量补齐路径后进入运行链路，约 2.2 秒后报 `Select a model before sending a message`；无回复 |
| CodeBuddy CN 4.12.2 | IDE 入口输出内核版本 1.106.1；`--help`、`chat --help` 均退出 0 | 已证实 IDE 命令行可启动；没有发送桌面聊天任务，未验证独立非交互执行 |
| Trae CN 3.4.4 | IDE 入口输出内核版本 1.107.1；`--help`、`chat --help` 均退出 0 | 同上；独立 `traecli` 未在当前 PATH 找到 |

版本号区分桌面 App 版本、IDE 内核版本和内置 AI CLI 版本，不能相互替代。

## 已确认的入口

均为绝对路径，不依赖 shell 中已安装同名命令：

| 入口 | 路径 |
| --- | --- |
| Node 运行时 | `/Users/wuhao/.nvmd/versions/22.19.0/bin/node`，本次实际版本 22.19.0 |
| WorkBuddy AI CLI | `/Applications/WorkBuddy.app/Contents/Resources/app.asar.unpacked/cli/bin/codebuddy` |
| WorkBuddy AI 国际包 CLI | `/Applications/WorkBuddy AI.app/Contents/Resources/app.asar.unpacked/cli/bin/codebuddy` |
| ZCode AI CLI | `/Applications/ZCode.app/Contents/Resources/glm/zcode.cjs` |
| CodeBuddy CN IDE CLI | `/Applications/CodeBuddy CN.app/Contents/Resources/app/bin/code`，帮助显示命令名 `buddycn` |
| Trae CN IDE CLI | `/Applications/Trae CN.app/Contents/Resources/app/bin/trae-cn` |

WorkBuddy 两个发行包仅携带 `codebuddy-lite-wb.mjs` 及对应分块。使用其启动脚本已有的 `CODEBUDDY_FORCE_LITE_WB_BUNDLE=1` 选择随包实现；这是本轮实测所需的启动条件。版本命令走快速路径，仅版本成功不能证明整个 bundle 可以加载，帮助检查已另行验证。

## 固定文本测试方式

测试输入只要求返回 `MAW_CLI_OK`，明确禁止调用工具、读取和写入文件。未提供任何项目内容。使用新建的临时工作目录、关闭标准输入、捕获输出，并设置超时；超时后向测试进程组发送终止信号。

WorkBuddy 两个包使用 `-p`、`--output-format json`、`--tools ""`、`--max-turns 1`、`--no-session-persistence`，同时设置 `CODEBUDDY_FORCE_LITE_WB_BUNDLE=1` 和 `CODEBUDDY_DISABLE_COMPILE_CACHE=1`。未开启权限跳过模式。

ZCode 使用 `--prompt`、`--mode plan`、`--json`，未沿用该版本 headless 默认的 yolo 模式。

首次受限环境中，两款 WorkBuddy 帮助启动有配置拉取 DNS 错误，固定文本调用分别在 20 秒超时。随后在正常联网环境重试，才得到明确的认证失败。因此不能把首次超时解释为 CLI 不兼容或用户没有登录桌面 App。

正常联网重试中，WorkBuddy 的认证失败同时表现为**进程退出码 0、stdout 为空、stderr 有错误**。后续接入器必须同时核对结构化结果、错误输出和超时状态，不能仅凭退出码判成功。

## ZCode 路径问题定位

默认执行尝试查找：

- `/Applications/ZCode.app/Contents/Resources/glm/provider/zcode-builtin.json`
- `/config/provider/zcode-builtin.json`

实际随包配置位于 `/Applications/ZCode.app/Contents/Resources/config/provider/zcode-builtin.json`。本轮将该随包配置复制到临时目录，通过代码中已存在的以下环境变量传入，未更改 App 文件：

- `ZCODE_BUILTIN_PROVIDER_CONFIG_FILE`：临时配置副本。
- `ZCODE_PERSONAL_PROVIDER_CONFIG_FILE`：已存在的 `/Users/wuhao/.zcode/v2/provider_config.json`，由 CLI 自行加载；未读取或输出其凭据内容。

这使调用越过配置定位错误，但随后触发“发送消息前未选择模型”的错误。本轮未更改模型选择或登录流程，也未将模型选择问题推断为账号未登录。CLI 自身可能保存运行日志或会话元数据；测试工作目录与临时配置已按临时目录生命周期清理。

## 对 maw 接入的直接影响

1. WorkBuddy 两个包具有真实可执行的随包 AI CLI，可复用同系列接入器；认证上下文需单独核实，不能默认继承桌面登录。
2. ZCode 有真实可执行的随包 CLI，但当前随包路径解析与模型选择都需要接入器启动契约处理；尚未跑通模型请求。
3. CodeBuddy CN 与 Trae CN 当前确认的是 IDE `chat` 入口。该入口的帮助列出窗口、模式与文件上下文选项，没有证实后台 JSON 结果回收能力。
4. 将节点标记为“入口可启动”“认证待处理”“模型未选择”比统一标记为“可执行”准确。
5. 后续最小验收应要求：收到固定回复、可归属运行批次、无工具调用、可确认结束；本轮仅完成了启动与失败原因定位。

本轮未调用登录／登出、未安装或升级软件、未向已打开桌面聊天发送消息、未派发 maw 任务。未验证取消传播、会话恢复、完整 Token 字段或实际代码任务。

## 后续：独立 CodeBuddy CLI 已安装

用户授权后执行 `npm install -g @tencent-ai/codebuddy-code --no-audit --no-fund`，安装成功，新增 105 个包。现有 CodeBuddy CN 桌面 App 未调整。

- 当前命令入口：`/Users/wuhao/.nvmd/versions/22.19.0/bin/codebuddy`。
- 新终端命令查询可找到 `codebuddy`；`codebuddy --version` 返回 `2.173.0`。
- 帮助正常退出，确认 print、JSON、禁用工具、最大轮次、不持久化会话及严格 MCP 配置选项存在。
- 正常联网环境的固定文本测试在约 2.4 秒内返回认证要求，退出码仍为 0，无模型回复。
- 使用统一官方 CLI 包；首次交互登录应选择 **Chinese Site**，对应国内站。未代用户执行登录或更换现有账号。

测试仍使用临时工作目录，禁用内置工具并传入空 MCP 配置及严格模式，没有发送项目内容。

官方来源：[安装指南](https://www.codebuddy.cn/docs/cli/installation)、[国内站登录说明](https://www.codebuddy.cn/docs/cli/quickstart)。
