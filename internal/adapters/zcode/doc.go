// Package zcode 为 ZCode 建立可解释的配置就绪与来源适配（VN-307）。
//
// 边界：
//
//  1. 运行时、provider 配置、模型就绪分三列报告，不塞进一个 online。
//  2. 路径补齐只走受控配置引用（profile_ref／显式绝对路径），不猜用户家目录秘密。
//  3. 未验证的模型调用、取消、恢复保持 unknown／unsupported，缺模型时不改用户配置。
//  4. 不复制账号秘密，不改变全局模型选择。
//
// 证据：docs/evidence/zcode。
package zcode
