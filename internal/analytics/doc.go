// Package analytics 把已投影的时间区间与已计量的用量/交付聚合成统一报表。
//
// 它是只读查询层：输入是 projection.Projector 产出的区间与一次一致读快照
// （*sql.Tx，见 internal/db Store.Read），输出带质量元数据的聚合结果；
// 不写库、不采集事实、不做归属判定（分摊见 internal/observability）。
//
// 聚合规则直接取自 timeline-metering-contract-v1 §4–§5：
//
//   - 时长按「同一身份」的并集计算，不是各区间相加；跨身份可以重叠，
//     聚合时按身份分别求并集后再相加（平均并发 = Σ 各身份并集秒数 / 窗口秒数）。
//   - token 按 turn 去重：同一 turn 同一 scope 只取最大 epoch 一条，
//     共享轮次只记一次；未知（quality=unknown 或 token 为 null）不填 0，
//     计入 turn_count 但不计入 token 总量，并置 usage_unknown。
//   - 零分母的比率一律为 0，不假装 1；缺数据如实标 unknown/partial，
//     报表必须带 watermark 与 completeness，缺任一字段视为不可比较。
//   - 查询范围（工程、来源、窗口、时区）随报表回显，搜索过滤后的子集
//     不与全集混算。
package analytics
