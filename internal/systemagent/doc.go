// Package systemagent 实现系统 Agent 反应总线：
// 规则编排（事件→分类→入队）+ LLM 诊断决策 + 确定性运维动作。
//
// 首期能力：unblock_assist（阻塞自愈）；同总线挂 task_split / ops_run。
package systemagent
