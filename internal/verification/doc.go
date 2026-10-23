// Package verification 把冻结规格上的验证策略跑成版本绑定的验收证据。
//
// 契约：maw.delivery.manifest/v1（验证必须对准任务版本与被测提交／产物）。
//
// 边界（VN-503）：
//
//  1. 只执行用户／工程定义的命令列表，不从模型输出拼 shell。
//  2. 结果保留命令、状态、证据；仅 exit_code 不能算通过。
//  3. 旧提交、产物哈希漂移、环境失联／超时、写盘越界 → 不能支撑 done。
//  4. 机器验证与人工结论分开：VerifyManual 需要人工标记，不由 gate 自动变成 pass。
package verification
