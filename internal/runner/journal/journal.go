package journal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Run 目录内的固定文件名。
const (
	SpecFile   = "spec.json"
	SpecMeta   = "spec.meta.json"
	JournalLog = "journal.jsonl"
	CursorFile = "cursor.json"
	StateFile  = "journal.state.json"
)

var (
	// ErrSpecFrozen 表示冻结规格已存在且内容不同：一份 Run 规格只能写一次。
	ErrSpecFrozen = errors.New("journal: 冻结规格不可改写")
	// ErrCorrupt 表示日志中间损坏（不是可恢复的撕裂尾部）。
	ErrCorrupt = errors.New("journal: 日志损坏")
	// ErrAlreadyStored 由提交方返回，表示该去重键已入库；日志据此推进游标。
	ErrAlreadyStored = errors.New("journal: 记录已入库")
	// ErrInvalidRecord 表示调用方给出的记录不可写入。
	ErrInvalidRecord = errors.New("journal: 记录不合法")
	// ErrClosed 表示日志已关闭。
	ErrClosed = errors.New("journal: 已关闭")
	// ErrCursor 表示游标越界或回退。
	ErrCursor = errors.New("journal: 游标非法")
)

// Source 是来源记录的身份：有厂商稳定 ID 时按命名空间去重，
// 没有时按 Run、解析版本、日志位置和记录索引形成可重复键。
type Source struct {
	Namespace    string `json:"namespace,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	ParseVersion string `json:"parse_version,omitempty"`
	Path         string `json:"path,omitempty"`
	RecordIndex  int    `json:"record_index,omitempty"`
}

// Record 是一条可恢复的语义记录。Seq 是稳定的本地序号（严格递增），
// Offset/Line 是它在 journal.jsonl 中的稳定位置。
type Record struct {
	Seq        int64           `json:"seq"`
	Offset     int64           `json:"offset"`
	Line       int             `json:"line"`
	Kind       string          `json:"kind"`
	DedupeKey  string          `json:"dedupe_key"`
	Source     Source          `json:"source"`
	OccurredAt *time.Time      `json:"occurred_at,omitempty"`
	ReceivedAt time.Time       `json:"received_at"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	// Redacted 表示写入时发生过秘密替换；载荷不再完整。
	Redacted bool `json:"redacted,omitempty"`
	// Incomplete 表示该条记录不足以支撑完整重建（例如来源自身截断）。
	Incomplete bool `json:"incomplete,omitempty"`
}

// DeriveDedupeKey 生成稳定去重键：不按文本哈希，因为相同文本可能是两条独立记录。
func DeriveDedupeKey(runID string, src Source) string {
	if src.Namespace != "" && src.EventID != "" {
		return src.Namespace + "/" + src.EventID
	}
	return strings.Join([]string{
		runID,
		src.ParseVersion,
		src.Path,
		strconv.Itoa(src.RecordIndex),
	}, "|")
}

// Cursor 是补交游标：Seq 之前的记录都已确认交付。
type Cursor struct {
	Seq       int64     `json:"seq"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Status 是本机日志的可观察事实；裁剪与撕裂造成的缺失在此显式可见。
type Status struct {
	Records            int   `json:"records"`
	Bytes              int64 `json:"bytes"`
	CursorSeq          int64 `json:"cursor_seq"`
	PendingRecords     int   `json:"pending_records"`
	TruncatedTailBytes int64 `json:"truncated_tail_bytes"`
	PrunedRecords      int64 `json:"pruned_records"`
	PrunedBytes        int64 `json:"pruned_bytes"`
	OverBudget         bool  `json:"over_budget"`
	Incomplete         bool  `json:"incomplete"`
}

type persistedState struct {
	// LastSeq 是已发放过的最大序号；即使记录被裁剪，序号也不得回退。
	LastSeq            int64     `json:"last_seq"`
	TruncatedTailBytes int64     `json:"truncated_tail_bytes"`
	PrunedRecords      int64     `json:"pruned_records"`
	PrunedBytes        int64     `json:"pruned_bytes"`
	OverBudget         bool      `json:"over_budget"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// Options 是打开日志的参数。MaxBytes 为 0 表示不设上限；Sync 为真时每条记录落盘后 fsync。
type Options struct {
	RunID    string
	MaxBytes int64
	Sync     bool
	Now      func() time.Time
}

// Journal 是单个 Run 的本机语义日志。并发调用由内部互斥保护。
type Journal struct {
	dir      string
	runID    string
	maxBytes int64
	sync     bool
	now      func() time.Time

	mu         sync.Mutex
	file       *os.File
	records    []Record
	nextOffset int64
	cursor     Cursor
	state      persistedState
	closed     bool
}

// Open 打开（必要时创建）Run 目录中的日志，并恢复撕裂尾部与保留事实。
func Open(dir string, opts Options) (*Journal, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("%w: 缺少 Run 目录", ErrInvalidRecord)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("journal: 创建 Run 目录: %w", err)
	}
	path := filepath.Join(dir, JournalLog)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: 打开日志: %w", err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal: 读取日志: %w", err)
	}
	scan, err := scanRecords(data)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if scan.tornBytes > 0 {
		if err := f.Truncate(scan.validEnd); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("journal: 截断撕裂尾部: %w", err)
		}
	}
	if scan.needsNewline {
		if _, err := f.Write([]byte("\n")); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("journal: 修复末行换行: %w", err)
		}
		scan.validEnd++
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("journal: 定位日志末尾: %w", err)
	}

	j := &Journal{
		dir:        dir,
		runID:      opts.RunID,
		maxBytes:   opts.MaxBytes,
		sync:       opts.Sync,
		now:        opts.Now,
		file:       f,
		records:    scan.records,
		nextOffset: scan.validEnd,
	}
	if scan.tornBytes > 0 {
		j.state.TruncatedTailBytes += scan.tornBytes
	}
	if err := j.loadState(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := j.loadCursor(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if scan.tornBytes > 0 {
		if err := j.saveState(); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return j, nil
}

// Append 追加一条语义记录：分配稳定序号与位置，过滤秘密，必要时按预算裁剪。
func (j *Journal) Append(rec Record) (Record, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return Record{}, ErrClosed
	}
	if strings.TrimSpace(rec.Kind) == "" {
		return Record{}, fmt.Errorf("%w: kind 不得为空", ErrInvalidRecord)
	}
	if rec.Seq != 0 {
		return Record{}, fmt.Errorf("%w: 序号由日志分配", ErrInvalidRecord)
	}
	if len(rec.Payload) > 0 {
		clean, n, err := RedactJSON(rec.Payload)
		if err != nil {
			return Record{}, fmt.Errorf("%w: %v", ErrInvalidRecord, err)
		}
		rec.Payload = clean
		if n > 0 {
			rec.Redacted = true
		}
	}
	if rec.DedupeKey == "" {
		rec.DedupeKey = DeriveDedupeKey(j.runID, rec.Source)
	}
	if rec.ReceivedAt.IsZero() {
		rec.ReceivedAt = j.now().UTC()
	} else {
		rec.ReceivedAt = rec.ReceivedAt.UTC()
	}
	if rec.OccurredAt != nil {
		at := rec.OccurredAt.UTC()
		rec.OccurredAt = &at
	}

	// 先裁剪已交付的旧记录，再为当前记录定位；未交付记录永不删除。
	if j.maxBytes > 0 && j.nextOffset+int64(len(rec.Payload))+256 > j.maxBytes {
		if _, err := j.pruneLocked(); err != nil {
			return Record{}, err
		}
	}

	rec.Seq = j.nextSeq()
	rec.Offset = j.nextOffset
	rec.Line = len(j.records) + 1
	line, err := json.Marshal(rec)
	if err != nil {
		return Record{}, fmt.Errorf("journal: 序列化记录: %w", err)
	}
	line = append(line, '\n')
	n, err := j.file.Write(line)
	if err != nil {
		return Record{}, fmt.Errorf("journal: 追加记录: %w", err)
	}
	if n != len(line) {
		return Record{}, fmt.Errorf("journal: 追加记录: %w", io.ErrShortWrite)
	}
	if j.sync {
		if err := j.file.Sync(); err != nil {
			return Record{}, fmt.Errorf("journal: 刷盘: %w", err)
		}
	}
	j.nextOffset += int64(n)
	j.records = append(j.records, rec)
	return rec, nil
}

// Records 返回当前仍在日志中的记录快照（按物理顺序）。
func (j *Journal) Records() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, len(j.records))
	copy(out, j.records)
	return out
}

// Cursor 返回当前补交游标。
func (j *Journal) Cursor() Cursor {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cursor
}

// Pending 返回尚未确认交付的记录，按序号升序。
func (j *Journal) Pending() []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Record, 0, len(j.records))
	for _, rec := range j.records {
		if rec.Seq > j.cursor.Seq {
			out = append(out, rec)
		}
	}
	return out
}

// Checkpoint 原子推进补交游标；只能前进且不得越过最后一条记录。
func (j *Journal) Checkpoint(seq int64) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrClosed
	}
	if seq < j.cursor.Seq {
		return fmt.Errorf("%w: 不能回退 %d -> %d", ErrCursor, j.cursor.Seq, seq)
	}
	if floor := j.seqFloor(); seq > floor {
		return fmt.Errorf("%w: %d 超过已发放序号 %d", ErrCursor, seq, floor)
	}
	return j.checkpointLocked(seq)
}

// Status 返回日志的可观察状态。
func (j *Journal) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	pending := 0
	for _, rec := range j.records {
		if rec.Seq > j.cursor.Seq {
			pending++
		}
	}
	return Status{
		Records:            len(j.records),
		Bytes:              j.nextOffset,
		CursorSeq:          j.cursor.Seq,
		PendingRecords:     pending,
		TruncatedTailBytes: j.state.TruncatedTailBytes,
		PrunedRecords:      j.state.PrunedRecords,
		PrunedBytes:        j.state.PrunedBytes,
		OverBudget:         j.state.OverBudget,
		Incomplete:         j.state.TruncatedTailBytes > 0 || j.state.OverBudget,
	}
}

// Submitter 把一条记录写入持久事实（通常是数据库）。返回 ErrAlreadyStored
// 表示同键记录已入库，日志会推进游标但不计为新的交付。
type Submitter interface {
	Submit(ctx context.Context, rec Record) error
}

// SubmitterFunc 让函数满足 Submitter。
type SubmitterFunc func(context.Context, Record) error

// Submit 实现 Submitter。
func (f SubmitterFunc) Submit(ctx context.Context, rec Record) error { return f(ctx, rec) }

// ResubmitReport 是一次补交的结果。
type ResubmitReport struct {
	Delivered  int
	Duplicates int
}

// Resubmit 按序号顺序补交所有待交付记录；失败即停在该条，游标不越过它。
// 无法确认数据库提交与本地游标的跨介质原子性，因此重复窗口由调用方按
// DedupeKey 幂等去重，或以 ErrAlreadyStored 回传已经入库的事实。
func (j *Journal) Resubmit(ctx context.Context, sub Submitter) (ResubmitReport, error) {
	if sub == nil {
		return ResubmitReport{}, fmt.Errorf("%w: 缺少提交方", ErrInvalidRecord)
	}
	var rep ResubmitReport
	for {
		pending := j.Pending()
		if len(pending) == 0 {
			return rep, nil
		}
		rec := pending[0]
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		err := sub.Submit(ctx, rec)
		switch {
		case err == nil:
			rep.Delivered++
		case errors.Is(err, ErrAlreadyStored):
			rep.Duplicates++
		default:
			return rep, fmt.Errorf("journal: 补交序号 %d 失败: %w", rec.Seq, err)
		}
		if err := j.Checkpoint(rec.Seq); err != nil {
			return rep, err
		}
	}
}

// PruneReport 是一次裁剪的结果。OverBudget 为真表示即使删光已交付记录，
// 未交付记录仍超出预算；它们被保留，缺失在 Status 中显式可见。
type PruneReport struct {
	Removed     int
	BytesBefore int64
	BytesAfter  int64
	OverBudget  bool
	PendingKept int
}

// Prune 按 MaxBytes 删除已交付的旧记录，绝不删除未交付记录。
func (j *Journal) Prune() (PruneReport, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return PruneReport{}, ErrClosed
	}
	return j.pruneLocked()
}

// Close 刷盘并关闭日志。
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	if err := j.file.Sync(); err != nil {
		_ = j.file.Close()
		return err
	}
	return j.file.Close()
}

func (j *Journal) pendingCount() int {
	n := 0
	for _, rec := range j.records {
		if rec.Seq > j.cursor.Seq {
			n++
		}
	}
	return n
}

// seqFloor 是已发放的最大序号：裁剪后的记录由持久状态和游标记住。
func (j *Journal) seqFloor() int64 {
	floor := j.lastSeq()
	if j.state.LastSeq > floor {
		floor = j.state.LastSeq
	}
	if j.cursor.Seq > floor {
		floor = j.cursor.Seq
	}
	return floor
}

// nextSeq 保证序号跨裁剪仍单调。
func (j *Journal) nextSeq() int64 { return j.seqFloor() + 1 }

func (j *Journal) lastSeq() int64 {
	if len(j.records) == 0 {
		return 0
	}
	return j.records[len(j.records)-1].Seq
}

func (j *Journal) checkpointLocked(seq int64) error {
	if seq == j.cursor.Seq {
		return nil
	}
	next := Cursor{Seq: seq, UpdatedAt: j.now().UTC()}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(j.dir, CursorFile), data, 0o600); err != nil {
		return fmt.Errorf("journal: 写游标: %w", err)
	}
	j.cursor = next
	return nil
}

func (j *Journal) pruneLocked() (PruneReport, error) {
	rep := PruneReport{BytesBefore: j.nextOffset, BytesAfter: j.nextOffset, PendingKept: j.pendingCount()}
	if j.maxBytes <= 0 || j.nextOffset <= j.maxBytes {
		return rep, nil
	}
	// 已交付记录总是前缀；一旦遇到未交付记录就不再向后删。
	var sizes []int64
	for i, rec := range j.records {
		end := j.nextOffset
		if i+1 < len(j.records) {
			end = j.records[i+1].Offset
		}
		sizes = append(sizes, end-rec.Offset)
	}
	remaining := j.nextOffset
	remove := 0
	for remove < len(j.records) && remaining > j.maxBytes && j.records[remove].Seq <= j.cursor.Seq {
		remaining -= sizes[remove]
		remove++
	}
	if remove == 0 {
		rep.OverBudget = true
		j.state.OverBudget = true
		if err := j.saveState(); err != nil {
			return rep, err
		}
		return rep, nil
	}

	survivors := j.records[remove:]
	// 先持久化已发放的最大序号，再删除记录：中断也不会让序号回退。
	j.state.LastSeq = j.seqFloor()
	if err := j.saveState(); err != nil {
		return rep, err
	}
	var buf bytes.Buffer
	off := int64(0)
	out := make([]Record, 0, len(survivors))
	for i, rec := range survivors {
		rec.Offset = off
		rec.Line = i + 1
		line, err := json.Marshal(rec)
		if err != nil {
			return rep, fmt.Errorf("journal: 重写记录: %w", err)
		}
		line = append(line, '\n')
		buf.Write(line)
		off += int64(len(line))
		out = append(out, rec)
	}
	path := filepath.Join(j.dir, JournalLog)
	if err := writeFileAtomic(path, buf.Bytes(), 0o600); err != nil {
		return rep, fmt.Errorf("journal: 裁剪日志: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return rep, fmt.Errorf("journal: 重开日志: %w", err)
	}
	old := j.file
	j.file = f
	_ = old.Close()
	j.records = out
	j.nextOffset = off

	var removedBytes int64
	for _, s := range sizes[:remove] {
		removedBytes += s
	}
	j.state.PrunedRecords += int64(remove)
	j.state.PrunedBytes += removedBytes
	j.state.OverBudget = off > j.maxBytes
	if err := j.saveState(); err != nil {
		return rep, err
	}
	rep.Removed = remove
	rep.BytesAfter = off
	rep.OverBudget = j.state.OverBudget
	rep.PendingKept = j.pendingCount()
	return rep, nil
}

func (j *Journal) loadState() error {
	data, err := os.ReadFile(filepath.Join(j.dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("journal: 读取保留事实: %w", err)
	}
	var st persistedState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("journal: 保留事实损坏: %w", err)
	}
	j.state.PrunedRecords += st.PrunedRecords
	j.state.PrunedBytes += st.PrunedBytes
	j.state.OverBudget = st.OverBudget
	return nil
}

func (j *Journal) loadCursor() error {
	data, err := os.ReadFile(filepath.Join(j.dir, CursorFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("journal: 读取游标: %w", err)
	}
	var c Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("journal: 游标损坏: %w", err)
	}
	if c.Seq < 0 || c.Seq > j.seqFloor() {
		return fmt.Errorf("%w: 游标 %d 超出日志范围", ErrCursor, c.Seq)
	}
	j.cursor = c
	return nil
}

func (j *Journal) saveState() error {
	st := j.state
	st.UpdatedAt = j.now().UTC()
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(j.dir, StateFile), data, 0o600); err != nil {
		return fmt.Errorf("journal: 写保留事实: %w", err)
	}
	return nil
}

type scanResult struct {
	records      []Record
	validEnd     int64
	tornBytes    int64
	needsNewline bool
}

// scanRecords 恢复日志：中间的坏行是损坏，末尾未写完的半条是可恢复的撕裂尾部。
func scanRecords(data []byte) (scanResult, error) {
	var res scanResult
	var offset int64
	lineNo := 0
	var lastSeq int64
	for offset < int64(len(data)) {
		lineNo++
		idx := bytes.IndexByte(data[offset:], '\n')
		complete := idx >= 0
		var line []byte
		var next int64
		if complete {
			line = data[offset : offset+int64(idx)]
			next = offset + int64(idx) + 1
		} else {
			line = data[offset:]
			next = int64(len(data))
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if !complete {
				res.tornBytes = int64(len(data)) - offset
				return res, nil
			}
			offset = next
			continue
		}
		rec, err := decodeRecord(line)
		if err != nil {
			if !complete {
				res.tornBytes = int64(len(data)) - offset
				return res, nil
			}
			return res, fmt.Errorf("%w: 第 %d 行: %v", ErrCorrupt, lineNo, err)
		}
		if rec.Offset != offset || rec.Line != lineNo {
			return res, fmt.Errorf("%w: 第 %d 行位置不匹配（记录 %d/%d，实际 %d/%d）",
				ErrCorrupt, lineNo, rec.Offset, rec.Line, offset, lineNo)
		}
		if rec.Seq <= lastSeq {
			return res, fmt.Errorf("%w: 第 %d 行序号 %d 未严格递增", ErrCorrupt, lineNo, rec.Seq)
		}
		res.records = append(res.records, rec)
		lastSeq = rec.Seq
		offset = next
		res.validEnd = offset
		if !complete {
			// 完整 JSON 只缺末尾换行：保留记录并修复换行，避免下次追加粘连。
			res.needsNewline = true
			return res, nil
		}
	}
	res.validEnd = offset
	return res, nil
}

func decodeRecord(line []byte) (Record, error) {
	var rec Record
	if err := json.Unmarshal(line, &rec); err != nil {
		return Record{}, err
	}
	if rec.Seq <= 0 || strings.TrimSpace(rec.Kind) == "" {
		return Record{}, fmt.Errorf("缺少序号或类型")
	}
	if rec.DedupeKey == "" {
		return Record{}, fmt.Errorf("缺少去重键")
	}
	return rec, nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if name != "" {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	name = ""
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
