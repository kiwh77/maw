---
alwaysApply: true
description: maw multi-agent protocol
---

<!-- maw:begin v7 -->
## 多 Agent 协议（由 maw 管理，请勿手改）
1. 状态库在本机全局 `~/.maw/maw.db`；本目录 `.multiagent/` 仅存配置与蓝图。多工程共用同一看板。
2. 先设定身份并注册：Worker 名统一为 `{应用}-{工程}-{序号}`（工程名去连字符小写，例 `cursor-demo-1`）。`export MAW_AGENT=<名>` 后 `maw register`；未指定时 `maw register` / `maw next` 会按当前应用自动分配。应用可用 `MAW_APP` 覆盖。
3. 运行 `maw next` 领取任务，只在该 Agent 的持久 worktree（`.worktrees/{agent}`）中工作；maw 会记录你正在执行哪道题。
4. 只修改任务卡 write_scope 内的文件；需要越界时运行 `maw request-change`。
5. 里程碑运行 `maw progress`；非编排链路的进度可用 `maw status report` 注入本机中枢。完成运行 `maw submit`，无法继续运行 `maw block`。
6. 换人或中断前运行 `maw handoff`；本会话无更多可领任务时再 `maw unregister`。
7. 完成 submit 后可自行执行 `maw merge --yes` 合入；合入成功保留 Agent worktree、仅删除题分支；禁止直接用 git merge/rebase/push 主干。
8. 合入完成后若仍有 ready 任务，继续 `maw next`（同树 reset 到主干 tip 后换新题分支）循环直至无题可领或用户叫停。
9. 全局看板：`maw board --serve`，可按工程/Agent/状态筛选；残留可用 `maw prune` 清理。
10. 出错时先读 maw 输出里的「下一步」提示，不要绕过。
<!-- maw:end -->
