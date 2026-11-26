package events

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// reKind 是标准事件 kind 的形态：命名空间.动作，如 run.phase、turn.finished。
// 裸词（旧记录的 progress、claim）一律不合规，必须先归一到带命名空间的形式。
var reKind = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$`)

// Result 是一批观测的摄取结论。四类互斥：每条观测只会落在其中一个切片里。
type Result struct {
	Accepted   []Record
	Duplicates []Duplicate
	Conflicts  []Conflict
	Rejected   []Rejection
}

// Events 返回本批被接受的标准事件，供上层直接落库。
func (r Result) Events() []model.Event {
	out := make([]model.Event, 0, len(r.Accepted))
	for _, rec := range r.Accepted {
		out = append(out, rec.Event)
	}
	return out
}

// Empty 报告本批是否什么都没发生（无接受、无重放、无冲突、无拒收）。
func (r Result) Empty() bool {
	return len(r.Accepted) == 0 && len(r.Duplicates) == 0 && len(r.Conflicts) == 0 && len(r.Rejected) == 0
}

// Rejection 是一条未被接受的观测。Index 是它在批次里的位置，便于按行反馈。
type Rejection struct {
	Index     int    `json:"index"`
	RecordRef string `json:"record_ref,omitempty"`
	Reason    Reason `json:"reason"`
	Field     string `json:"field,omitempty"`
	Msg       string `json:"msg,omitempty"`
}

// Error 把拒收映射成带分类的端口错误，上层据此决定重试还是终止。
func (r Rejection) Error() string {
	msg := r.Msg
	if msg == "" {
		msg = r.Reason.Title()
	}
	if r.RecordRef == "" {
		return fmt.Sprintf("%s: %s", r.Reason, msg)
	}
	return fmt.Sprintf("%s %s: %s", r.Reason, r.RecordRef, msg)
}

// Err 返回可 errors.Is 判定的类型化错误；原因为空时返回 nil。
func (r Rejection) Err() *model.Error {
	if r.Reason == "" {
		return nil
	}
	msg := r.Msg
	if msg == "" {
		msg = r.Reason.Title()
	}
	return r.Reason.Error(msg)
}

// Duplicate 是同键同内容的重放。EventID 是首次接受时的事件身份，重放不新建。
type Duplicate struct {
	Index     int           `json:"index"`
	RecordRef string        `json:"record_ref,omitempty"`
	Key       string        `json:"source_key"`
	EventID   model.EventID `json:"event_id"`
}

// Conflict 是同键不同内容的争用。两侧都不覆盖：Existing 是已入库的事件，
// Incoming 是本批被拒的新说法，交人工判断哪个才是真的。
type Conflict struct {
	Index     int           `json:"index"`
	RecordRef string        `json:"record_ref,omitempty"`
	Key       string        `json:"source_key"`
	Existing  model.EventID `json:"existing_event_id"`
	Reason    Reason        `json:"reason"`
}

// Options 是摄取器的可替换依赖。
type Options struct {
	// Now 返回当前时间，用作接收时间。空则 time.Now。
	// 接收时间是我们自己的观测，来源给的 ReceivedAt 只有在其可信时才参考。
	Now func() time.Time
	// NextCursor 分配事件游标。空则内部递增（从 Seed 恢复的水位之后继续）。
	// 真实落库时由存储层分配（run_events.cursor 自增），这里只保证本进程内单调。
	NextCursor func() model.Cursor
}

// Ingestor 把观测写成事件，并在内存里维护分域去重索引与各域发生时间前沿。
//
// 并发安全（内部加锁）。但它不是持久层：进程重启后必须用 Seed 从已落库事件
// 重建索引，否则幂等会退化成「每次重放都新建一条」。
type Ingestor struct {
	mu         sync.Mutex
	now        func() time.Time
	nextCursor func() model.Cursor
	idx        *index
}

// New 构造摄取器。
func New(opts Options) *Ingestor {
	g := &Ingestor{now: opts.Now, nextCursor: opts.NextCursor, idx: newIndex()}
	if g.now == nil {
		g.now = time.Now
	}
	return g
}

// Frontier 返回某来源已见的最大发生时间（迟到判定基准）。
func (g *Ingestor) Frontier(src Source) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.idx.frontierOf(src.scope())
}

// LastCursor 返回已发出的最大事件游标。
func (g *Ingestor) LastCursor() model.Cursor {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.idx.maxCursor
}

// Seed 用已落库的事件记录重建某来源的去重索引与前沿。
//
// 这是「重启后幂等仍成立」的唯一途径：Record 里的 Key 与 Fingerprint 必须来自
// 落库载荷（Record.Payload），不能由内容重算——重算会把已接受的合法重复发生
// 误判成重放。
func (g *Ingestor) Seed(src Source, records []Record) []Rejection {
	if errs := src.Validate(); len(errs) > 0 {
		return []Rejection{{Reason: ReasonSourceUnbound, Field: "source", Msg: model.Err(errs).Error()}}
	}
	scope := src.scope()

	g.mu.Lock()
	defer g.mu.Unlock()

	var out []Rejection
	for i, rec := range records {
		if rec.Key == "" || rec.Fingerprint == "" {
			out = append(out, Rejection{Index: i, RecordRef: rec.RecordRef,
				Reason: ReasonSeedIncomplete, Field: "record",
				Msg: "重建索引需要 source_key 与 fingerprint"})
			continue
		}
		g.idx.register(scope, entry{
			eventID:     rec.Event.EventID,
			fingerprint: rec.Fingerprint,
			occurredAt:  rec.Event.OccurredAt,
			cursor:      rec.Event.Cursor,
			key:         rec.Key,
		})
	}
	return out
}

// Ingest 摄取一批观测。逐条独立判定，一条失败不影响同批其他条。
//
// 判定顺序刻意固定：先证「能解释」（记录、时间、质量），再证「有权限」（越权对象），
// 再证「不重复」（分域去重），最后才分配游标。这样任何一次拒收都不会白占一个游标，
// 也不会因为拒收而推进前沿。
func (g *Ingestor) Ingest(src Source, batch []Observation) Result {
	var res Result

	g.mu.Lock()
	defer g.mu.Unlock()

	if errs := src.Validate(); len(errs) > 0 {
		msg := model.Err(errs).Error()
		if len(batch) == 0 {
			res.Rejected = append(res.Rejected, Rejection{Reason: ReasonSourceUnbound, Field: "source", Msg: msg})
			return res
		}
		for i, o := range batch {
			res.Rejected = append(res.Rejected, Rejection{Index: i, RecordRef: o.RecordRef,
				Reason: ReasonSourceUnbound, Field: "source", Msg: msg})
		}
		return res
	}

	scope := src.scope()
	for i := range batch {
		o := batch[i]
		reject := func(reason Reason, field, msg string) {
			res.Rejected = append(res.Rejected, Rejection{Index: i, RecordRef: o.RecordRef,
				Reason: reason, Field: field, Msg: msg})
		}

		// 1. 能解释：记录位置、类型、版本、kind。
		if o.RecordRef == "" {
			reject(ReasonRecordRefMissing, "record_ref", "必须带稳定日志位置，事后才能回溯原始字节")
			continue
		}
		if o.RecordType == "" {
			reject(ReasonRecordTypeMissing, "record_type", "必须写明来源记录类型")
			continue
		}
		if o.SchemaVersion < 1 {
			reject(ReasonSchemaUnversioned, "schema_version", "来源 schema 版本必须为正整数")
			continue
		}
		if o.Kind == "" {
			reject(ReasonKindMissing, "kind", "必须给出标准事件 kind")
			continue
		}
		if !reKind.MatchString(o.Kind) {
			reject(ReasonKindMalformed, "kind", "kind 必须是 命名空间.动作，裸词需先用 DecodeLegacy 归一")
			continue
		}

		// 2. 时间：发生时间必填，接收时间由我们现取。
		if o.OccurredAt == nil || o.OccurredAt.IsZero() {
			reject(ReasonOccurredAtMissing, "occurred_at", "缺发生时间时不拿接收时间冒充")
			continue
		}
		occurred := o.OccurredAt.UTC()
		received := o.ReceivedAt.UTC()
		if received.IsZero() {
			received = g.now().UTC()
		}

		// 3. 质量。
		quality := o.Quality
		if quality == "" {
			quality = model.QualityObserved
		}
		if !quality.Valid() {
			reject(ReasonQualityInvalid, "quality", "只能是 observed/estimated/unknown")
			continue
		}

		// 4. 归属字段形态：run 与 generation 成对，阶段与状态属封闭集合。
		if (o.RunID == "") != (o.Generation == nil) {
			reject(ReasonRunGenerationUnpaired, "run_id", "run_id 与 generation 必须成对出现")
			continue
		}
		if o.Phase != nil && !o.Phase.Valid() {
			reject(ReasonPhaseUnknown, "phase", "运行阶段不在十个封闭值内")
			continue
		}
		if o.TaskStatus != nil && !o.TaskStatus.Valid() {
			reject(ReasonTaskStatusUnknown, "task_status", "任务状态不在十一个封闭值内")
			continue
		}
		if o.Claim != nil && !o.Claim.Instance.Valid() {
			reject(ReasonClaimUnbound, "claim.task_instance", "领取声明必须带合法任务实例")
			continue
		}

		// 5. 载荷与 kind 错位：两个方向都拦。
		//    反向（kind 说要带值却没带）同样是脏数据：一条 run.phase 事件没有阶段值，
		//    事后无法与「当前阶段」对上（E260），等于把缺字段记成了一次真实执行。
		if o.Phase != nil && o.Kind != KindRunPhase {
			reject(ReasonPayloadKindMismatch, "phase", "只有 kind=run.phase 才能携带运行阶段")
			continue
		}
		if o.TaskStatus != nil && o.Kind != KindTaskStatus {
			reject(ReasonPayloadKindMismatch, "task_status", "只有 kind=task.status 才能携带任务状态")
			continue
		}
		if o.Claim != nil && o.Kind != KindClaimRequested {
			reject(ReasonPayloadKindMismatch, "claim", "只有 kind=claim.requested 才能携带领取声明")
			continue
		}
		switch o.Kind {
		case KindRunPhase:
			if o.Phase == nil {
				reject(ReasonPayloadKindMismatch, "phase", "kind=run.phase 必须带阶段值")
				continue
			}
		case KindTaskStatus:
			if o.TaskStatus == nil {
				reject(ReasonPayloadKindMismatch, "task_status", "kind=task.status 必须带任务状态值")
				continue
			}
		case KindClaimRequested:
			if o.Claim == nil {
				reject(ReasonPayloadKindMismatch, "claim", "kind=claim.requested 必须带领取声明")
				continue
			}
		}

		// 6. 越权：观测域没有任何控制权，也没有所有权。
		// 先看它想改什么，再看 kind：声称「任务已完成」比泛泛写控制类事实更该被点名。
		if !src.Domain.CanControl() {
			switch {
			case o.TaskStatus != nil:
				reject(ReasonAuthorityEscalation, "task_status",
					fmt.Sprintf("旁路上报不取得任务所有权，不得把任务置为 %s", *o.TaskStatus))
				continue
			case o.Claim != nil:
				reject(ReasonAuthorityEscalation, "claim", "旁路上报不取得任务所有权")
				continue
			case o.Phase != nil:
				reject(ReasonAuthorityEscalation, "phase", "旁路上报不得声明运行阶段")
				continue
			case controlKinds[o.Kind]:
				reject(ReasonAuthorityEscalation, "kind",
					fmt.Sprintf("接入域 %s 不能写控制类事实 %s", src.Domain, o.Kind))
				continue
			}
		}
		if o.Claim != nil && src.Agent != "" && o.Claim.Agent != src.Agent {
			reject(ReasonClaimAgentMismatch, "claim.agent",
				fmt.Sprintf("上报者 %s 不能替 %s 领取", src.Agent, o.Claim.Agent))
			continue
		}

		// 7. 质量降级：发生晚于接收＝来源时钟不可信；旧字段 ts 是入库时刻。
		// 降级只在声称 observed 时发生，但说明一律留下——即便来源自己已经说了 estimated，
		// 读者也该知道「这个 estimated 是因为旧 ts 不可信」。
		var notes []string
		if occurred.After(received) {
			if quality == model.QualityObserved {
				quality = model.QualityEstimated
			}
			notes = append(notes, NoteClockSkew)
		}
		if o.Legacy {
			if quality == model.QualityObserved {
				quality = model.QualityEstimated
			}
			notes = append(notes, NoteLegacyTimestamp)
		}

		// 8. 归属：观测域一律未归属，声明值只留在载荷里。
		reported := Reported{
			Instance: o.Instance, RunID: o.RunID, Generation: o.Generation,
			Phase: o.Phase, TaskStatus: o.TaskStatus,
		}
		attribution, runForEvent := Attributed, o.RunID
		switch {
		case !src.Domain.CanControl():
			attribution, runForEvent = Unattributed, ""
		case o.RunID == "" && !o.Instance.Valid():
			attribution = Unattributed
		}

		// 9. 分域去重：键在域内唯一；内容不同即冲突，不覆盖。
		key, derived := o.SourceKey, false
		if key == "" {
			key, derived = derivedKey(o, occurred), true
		}
		fingerprint := fingerprintOf(o, occurred)
		switch verdict, existing := g.idx.lookup(scope, key, fingerprint); verdict {
		case lookupReplay:
			res.Duplicates = append(res.Duplicates, Duplicate{
				Index: i, RecordRef: o.RecordRef, Key: key, EventID: existing.eventID})
			continue
		case lookupConflict:
			res.Conflicts = append(res.Conflicts, Conflict{
				Index: i, RecordRef: o.RecordRef, Key: key,
				Existing: existing.eventID, Reason: ReasonKeyConflict})
			continue
		}

		// 10. 游标只前进。
		cursor := g.allocate()
		if cursor <= g.idx.maxCursor {
			reject(ReasonCursorRegression, "cursor",
				fmt.Sprintf("分配到的游标 %d 不大于已发出的 %d", cursor, g.idx.maxCursor))
			continue
		}

		// 11. 迟到判定必须在登记之前：登记会把前沿推到本条。
		late := g.idx.isLate(scope, occurred)

		var eventPhase *model.RunPhase
		if o.Kind == KindRunPhase {
			eventPhase = o.Phase
		}
		event := model.Event{
			EventID:       eventIDFor(scope, key),
			Cursor:        cursor,
			SchemaVersion: o.SchemaVersion,
			Kind:          o.Kind,
			RunID:         runForEvent,
			Phase:         eventPhase,
			OccurredAt:    occurred,
			ReceivedAt:    received,
			Quality:       quality,
		}

		g.idx.register(scope, entry{
			eventID:     event.EventID,
			fingerprint: fingerprint,
			occurredAt:  occurred,
			cursor:      cursor,
			key:         key,
		})

		res.Accepted = append(res.Accepted, Record{
			Event:        event,
			Source:       src,
			RecordRef:    o.RecordRef,
			RecordType:   o.RecordType,
			Key:          key,
			KeyDerived:   derived,
			Fingerprint:  fingerprint,
			Attribution:  attribution,
			Reported:     reported,
			LateArrival:  late,
			Backfill:     o.Backfill,
			Legacy:       o.Legacy,
			QualityNotes: notes,
		})
	}
	return res
}

// allocate 取下一个游标：优先用外部分配器（真实存储），否则内部递增。
func (g *Ingestor) allocate() model.Cursor {
	if g.nextCursor != nil {
		return g.nextCursor()
	}
	return g.idx.maxCursor + 1
}
