package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// 标准事件 kind 中属于「控制」的几个封闭取值。
// 只有受管与人工域能写；观测域写了就是越权（ReasonAuthorityEscalation）。
const (
	KindRunPhase         = "run.phase"
	KindTaskStatus       = "task.status"
	KindLeaseAcquired    = "lease.acquired"
	KindLeaseReleased    = "lease.released"
	KindOperationUpdated = "operation.updated"
	KindClaimRequested   = "claim.requested"
)

// controlKinds 是控制类 kind 集合。
var controlKinds = map[string]bool{
	KindRunPhase:         true,
	KindTaskStatus:       true,
	KindLeaseAcquired:    true,
	KindLeaseReleased:    true,
	KindOperationUpdated: true,
	KindClaimRequested:   true,
}

// Claim 是「取得任务所有权」的显式声明。它本身不改任务状态：
// 真正的领取走 ClaimExecution 端口，本包只把声明写成 claim.requested 事件。
type Claim struct {
	Instance model.InstanceID `json:"task_instance"`
	Agent    model.AgentID    `json:"agent"`
}

// Observation 是一条待摄取的上报，也是 POST /observations 的线格式。
//
// 时间语义（契约 §3）：OccurredAt 是来源说的发生时间，必填；ReceivedAt 由摄取方
// 现取，来源给了也不采信（那是它自己的到达判断）。两者都原样保留，不互相顶替。
type Observation struct {
	// RecordRef 是稳定日志位置，事后必须能回溯到原始字节。
	RecordRef string `json:"record_ref"`
	// RecordType 是来源记录类型（如 stream.assistant、usage.snapshot）。
	RecordType string `json:"record_type"`
	// SchemaVersion 是来源记录的契约版本，从 1 开始。
	SchemaVersion int `json:"schema_version"`
	// SourceKey 是来源侧的稳定标识（消息 id、日志序号）。空则按内容+发生时间合成。
	SourceKey string `json:"source_key,omitempty"`
	// Kind 是目标标准事件 kind。
	Kind string `json:"kind"`

	OccurredAt *time.Time `json:"occurred_at,omitempty"`
	ReceivedAt time.Time  `json:"received_at,omitempty"`

	Instance   model.InstanceID  `json:"task_instance,omitempty"`
	RunID      model.RunID       `json:"run_id,omitempty"`
	Generation *model.Generation `json:"generation,omitempty"`
	Phase      *model.RunPhase   `json:"phase,omitempty"`
	TaskStatus *model.TaskStatus `json:"task_status,omitempty"`
	Claim      *Claim            `json:"claim,omitempty"`

	// Quality 是来源声称的证据质量；空值按「有发生时间即 observed」推定。
	Quality model.Quality `json:"quality,omitempty"`
	// Backfill 是来源声明的「日志补交」，允许回溯修正而不回退水位。
	Backfill bool `json:"backfill,omitempty"`
	// Legacy 标记已由旧字段归一化而来（旧 ts 是写入时刻，不是发生时刻）。
	Legacy bool `json:"legacy,omitempty"`

	// Payload 是来源原始附加字段，本包不解释、不改写，只随事件原样带走。
	Payload map[string]any `json:"payload,omitempty"`
}

// Reported 是来源声明的归属，未经核验。未归属的事件把它留在载荷里，
// 于是「来源说自己看到了什么」不会丢，事件本身却不因此获得归属。
type Reported struct {
	Instance   model.InstanceID  `json:"task_instance,omitempty"`
	RunID      model.RunID       `json:"run_id,omitempty"`
	Generation *model.Generation `json:"generation,omitempty"`
	Phase      *model.RunPhase   `json:"phase,omitempty"`
	TaskStatus *model.TaskStatus `json:"task_status,omitempty"`
}

// 质量降级说明，进 Record.QualityNotes 与载荷。
const (
	// NoteClockSkew 发生晚于接收：来源时钟不可信，只能记 estimated。
	NoteClockSkew = "clock_skew"
	// NoteLegacyTimestamp 旧字段的 ts 是入库时刻而非发生时刻，只能记 estimated。
	NoteLegacyTimestamp = "legacy_ts_is_receipt_time"
)

// Record 是一条已摄取的事件及其注解。Event 是落库用的标准事实，
// 其余字段说明这条事实从哪来、被降级过什么、是否迟到。
type Record struct {
	Event model.Event `json:"event"`

	Source Source `json:"source"`
	// RecordRef 与 RecordType 只在载荷里出现：事件本体没有位置放它们，
	// 由摄取器填进 Record，避免旁路字段污染 model.Event。
	RecordRef  string `json:"record_ref"`
	RecordType string `json:"record_type"`

	Key         string `json:"source_key"`
	KeyDerived  bool   `json:"key_derived"`
	Fingerprint string `json:"fingerprint"`

	Attribution  Attribution `json:"attribution"`
	Reported     Reported    `json:"reported,omitempty"`
	LateArrival  bool        `json:"late_arrival"`
	Backfill     bool        `json:"backfill"`
	Legacy       bool        `json:"legacy"`
	QualityNotes []string    `json:"quality_notes,omitempty"`
}

// HasNote 报告某条降级说明是否已记录。
func (r Record) HasNote(note string) bool {
	for _, n := range r.QualityNotes {
		if n == note {
			return true
		}
	}
	return false
}

// ingestPayload 是写入 db.payload_json 的注解体。
//
// 它必须自带 source_key 与 fingerprint：进程重启后要重建去重索引，只能靠已落库的
// 这些字段，不能靠内存；缺了它们，「重放一次就多一条」的洞会重新打开。
type ingestPayload struct {
	Source      Source         `json:"source"`
	Record      payloadRecord  `json:"record"`
	Ingest      payloadIngest  `json:"ingest"`
	Reported    Reported       `json:"reported,omitempty"`
	Attribution Attribution    `json:"attribution"`
	Time        payloadTime    `json:"time"`
	Raw         map[string]any `json:"raw,omitempty"`
}

type payloadRecord struct {
	Ref           string `json:"ref"`
	Type          string `json:"type"`
	SchemaVersion int    `json:"schema_version"`
}

type payloadIngest struct {
	SourceKey   string   `json:"source_key"`
	KeyDerived  bool     `json:"key_derived"`
	Fingerprint string   `json:"fingerprint"`
	Legacy      bool     `json:"legacy"`
	Notes       []string `json:"quality_notes,omitempty"`
}

type payloadTime struct {
	OccurredAt  time.Time `json:"occurred_at"`
	ReceivedAt  time.Time `json:"received_at"`
	LateArrival bool      `json:"late_arrival"`
	Backfill    bool      `json:"backfill"`
}

// Payload 生成入库载荷（写进 db 的 payload_json 列）。
// 编码稳定：map 键由 encoding/json 排序，同一 Record 反复编码结果一致。
func (r Record) Payload() ([]byte, error) {
	body := ingestPayload{
		Source: r.Source,
		Record: payloadRecord{
			Ref:           r.RecordRef,
			Type:          r.RecordType,
			SchemaVersion: r.Event.SchemaVersion,
		},
		Ingest: payloadIngest{
			SourceKey:   r.Key,
			KeyDerived:  r.KeyDerived,
			Fingerprint: r.Fingerprint,
			Legacy:      r.Legacy,
			Notes:       r.QualityNotes,
		},
		Reported:    r.Reported,
		Attribution: r.Attribution,
		Time: payloadTime{
			OccurredAt:  r.Event.OccurredAt,
			ReceivedAt:  r.Event.ReceivedAt,
			LateArrival: r.LateArrival,
			Backfill:    r.Backfill,
		},
	}
	return json.Marshal(body)
}

// canonicalMap 把任意 map 序列化成键有序的稳定串，供指纹与合成键使用。
// encoding/json 对 map 键排序，因此直接复用，不自己拼字符串。
func canonicalMap(m map[string]any) string {
	b, err := json.Marshal(m)
	if err != nil {
		// 不可序列化的载荷本来就不该入库；退化成空对象，至少让指纹可复现。
		return "{}"
	}
	return string(b)
}

// fingerprintOf 计算内容指纹，用于「同键是否同内容」的判定。
//
// 刻意排除：ReceivedAt（重放必然有新接收时间）、RecordRef（同一位置重读）、
// Backfill/Legacy（标注不改变内容）、SourceKey（它是键本身）。
// 收录 Quality：质量是这条事实的一部分，改了质量就是新说法，应判冲突。
func fingerprintOf(o Observation, occurred time.Time) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	write(o.Kind, strconvTime(occurred), string(o.Instance), string(o.RunID))
	write(genPtr(o.Generation), phasePtr(o.Phase), statusPtr(o.TaskStatus))
	write(string(o.Quality), canonicalMap(o.Payload))
	return hex.EncodeToString(h.Sum(nil))
}

// derivedKey 在来源没有稳定键时合成一个：同内容且同发生时间视为同一条记录的重放，
// 不同发生时间则是两次独立发生。合成键标记 key_derived，提示去重强度弱于来源键。
func derivedKey(o Observation, occurred time.Time) string {
	sum := sha256.Sum256([]byte(o.RecordType + "\x00" + o.Kind + "\x00" +
		strconvTime(occurred) + "\x00" + canonicalMap(o.Payload)))
	return "derived:" + hex.EncodeToString(sum[:8])
}

// eventIDFor 由「去重域 + 键」推导事件身份。
//
// 确定性 id 让幂等重放不必查库就能复用原 id，也让崩溃后重放不会多出一条事件。
func eventIDFor(scope, key string) model.EventID {
	sum := sha256.Sum256([]byte(scope + "\x00" + key))
	return model.EventID("evt_" + hex.EncodeToString(sum[:8]))
}

func strconvTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func genPtr(v *model.Generation) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(int(*v))
}

func phasePtr(p *model.RunPhase) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func statusPtr(s *model.TaskStatus) string {
	if s == nil {
		return ""
	}
	return string(*s)
}
