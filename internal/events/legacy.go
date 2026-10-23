package events

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 旧记录的归一常量。
const (
	// legacyRecordType 是归一后的来源记录类型。
	legacyRecordType = "legacy.event"
	// legacyKindPrefix 给裸词类型加命名空间，避免与标准控制类 kind 撞名。
	legacyKindPrefix = "legacy."
	// LegacyAdapter 是旧事件表在来源绑定里的接入器标识。
	LegacyAdapter = "legacy-events"
)

// legacyRow 是旧 events 表的列形状：ts、actor、type、task_id、data_json。
// 它没有 schema_version、没有发生／接收之分、也没有实例身份。
type legacyRow struct {
	TS     string         `json:"ts"`
	Actor  string         `json:"actor"`
	Type   string         `json:"type"`
	TaskID string         `json:"task_id"`
	Data   map[string]any `json:"data"`
}

// normalizeLegacyKind 把旧表的自由文本 type 归一成带 legacy 命名空间的 kind。
//
// 一律加前缀，即便是看起来合法的 run.phase 也一样：旧表没有任何契约约束，
// 一行 type=run.phase 只是某个调用方随手写的字符串，不能因此升级成控制事实。
func normalizeLegacyKind(typ string) string {
	s := strings.ToLower(strings.TrimSpace(typ))
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return legacyKindPrefix + s
}

// LegacySource 是旧事件表的来源绑定：一律观测域。
//
// 这不是保守，是事实——旧表由 `AppendEvent` 在写入时取当前时间（db.UTCNow），
// 既没有进程控制信息，也没有归属核验，因此只能当旁路回顾。
func LegacySource() Source {
	return Source{Domain: DomainObserved, Adapter: LegacyAdapter}
}

// DecodeLegacy 把旧 events 表的一行归一成观测。
//
// 两条硬规则：
//
//  1. **ts 当发生时间用，但质量只能是 estimated。** 旧表写的是入库时刻，
//     不是来源说的发生时刻（见 NoteLegacyTimestamp）；把它当 observed 会把
//     「我们什么时候记的」冒充成「事情什么时候发生的」。
//  2. **不猜实例。** 旧表只有 task_id（如 VN-802），那是显示编号，不是实例身份；
//     两者属于不同命名空间，因此旧记录一律未归属，task_id 原样留在载荷里。
func DecodeLegacy(ref string, raw []byte) (Observation, error) {
	var row legacyRow
	if err := json.Unmarshal(raw, &row); err != nil {
		return Observation{}, ReasonRecordRefMissing.Error("旧事件无法解析：" + err.Error())
	}
	if strings.TrimSpace(row.TS) == "" {
		return Observation{}, ReasonOccurredAtMissing.Error("旧事件缺 ts，不能凭空补发生时间")
	}
	occurred, err := time.Parse(time.RFC3339, row.TS)
	if err != nil {
		return Observation{}, ReasonOccurredAtMissing.Error("旧事件 ts 无法解析为 RFC3339：" + row.TS)
	}
	occurred = occurred.UTC()

	kind := normalizeLegacyKind(row.Type)
	if kind == legacyKindPrefix {
		return Observation{}, ReasonKindMissing.Error("旧事件 type 归一后为空")
	}

	payload := make(map[string]any, len(row.Data)+2)
	for k, v := range row.Data {
		payload[k] = v
	}
	if row.Actor != "" {
		payload["legacy_actor"] = row.Actor
	}
	if row.TaskID != "" {
		payload["legacy_task_id"] = row.TaskID
	}

	return Observation{
		RecordRef:     ref,
		RecordType:    legacyRecordType,
		SchemaVersion: 1,
		// 旧表主键（ref）就是稳定日志位置：重读同一行是重放，不是新事实。
		SourceKey:  ref,
		Kind:       kind,
		OccurredAt: &occurred,
		Quality:    model.QualityEstimated,
		Legacy:     true,
		Backfill:   true,
		Payload:    payload,
	}, nil
}
