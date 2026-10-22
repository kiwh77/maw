# Agent Skills（maw 工作流）

面向 Cursor / Claude Code 等支持 [Agent Skills](https://docs.cursor.com/context/skills) 的客户端：按阶段引导 Agent 正确使用 maw。

| 技能 | 作用 |
| --- | --- |
| [maw](maw/SKILL.md) | 系列路由与执行期速查 |
| [maw-init](maw-init/SKILL.md) | 工程初始化 / 协议升级 |
| [maw-task-list](maw-task-list/SKILL.md) | 拆可领取任务清单 |
| [maw-plan-push](maw-plan-push/SKILL.md) | 写蓝图并 `plan apply` |
| [maw-task-pull](maw-task-pull/SKILL.md) | `maw next` 本仓或跨仓领题 |

## 安装到本机

把本目录下各技能目录复制或软链到客户端的 skills 路径，例如：

```bash
# Cursor（用户技能）
ln -s "$(pwd)/skills/maw" ~/.cursor/skills/maw
ln -s "$(pwd)/skills/maw-init" ~/.cursor/skills/maw-init
ln -s "$(pwd)/skills/maw-task-list" ~/.cursor/skills/maw-task-list
ln -s "$(pwd)/skills/maw-plan-push" ~/.cursor/skills/maw-plan-push
ln -s "$(pwd)/skills/maw-task-pull" ~/.cursor/skills/maw-task-pull
```

若使用中央库（如 `~/.agents/skills`），链到中央库后再由各工具互通即可。
