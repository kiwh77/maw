// Package trae 按实测结果区分 Trae IDE 人工入口与独立 CLI 能力（VN-306）。
//
// 两条硬边界：
//
//  1. **只发现 IDE chat 时保持 manual。** 不得宣称可后台回收结果、不得开放受管启动。
//  2. **独立 CLI 不继承 IDE 权益。** 只有 Probe／参数／结果都通过同一契约用例、
//     且挂上本目录证据时，才把对应能力标为 observed 并允许 Launch。
//
// 非目标：不为凑验收安装 CLI；不默认桌面与 CLI 共享登录／模型／会话。
//
// 证据：docs/evidence/trae（能力矩阵与样本元数据）。G07：能力按版本验证，
// 换版本后 observed 降级为 unobserved。
package trae
