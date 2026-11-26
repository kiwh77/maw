# 自动执行开关 + Router 介入 · 验收证据

采集时间：2026-11-17（Asia/Shanghai），本机 `maw` 由 `./install.sh` 重装，
中枢 `maw serve --addr 127.0.0.1:7421` 常驻，浏览器 Chromium（Playwright）实测。

| 图 | 证明什么 |
| --- | --- |
| `toolbar-auto-run.png` | 工具栏「接入」左侧出现执行节点下拉 + 自动执行开关（开态：绿点＋「自动执行中」） |
| `confirm-dialog.png` | 打开前二次确认，明确写着「会启动本机 CLI 进程、写工作区文件、消耗 token」 |
| `router-problems.png` | 问题审查页把 `blocked` 与 `router`（已升级人工 + 模型给出的原因）分行呈现 |

## 同批跑通的机器证据

- 开关写入：`PUT /api/v1/projects/716c30878ba1b95f/auto-execution` → `auto_enabled`／
  `auto_select`／`allowed_nodes=["dsh-local"]`／`worker_node="dsh-local"` 一次写齐。
- 热生效：翻转开关后 2 秒内出现 `lead_switches_changed`
  `{"auto_select":true/false,"dispatch":…,"system_agent":…}`，**未重启 serve**。
- Router 决策：`maw system run --job …` 调真实 `claude -p`，产出
  `{"action":"escalate_human","reason":"…needs_human…"}` 并落 `decision.raw`。
- `ops_run` 审计：结果含 `ops_command`／`ops_allowlist_rule="maw prune"`／`ops_exit_code=0`／`stdout`。

## 复跑

```sh
./install.sh && maw serve --addr 127.0.0.1:7421
curl -s -c /tmp/ck -X POST http://127.0.0.1:7421/api/session
curl -s -b /tmp/ck http://127.0.0.1:7421/api/v1/projects/<project_id>/auto-execution
go test ./... && node --test internal/board/web/src/lib/problems.test.cjs
```
