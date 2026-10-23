package journal

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func openTestJournal(t *testing.T, dir string, opts Options) *Journal {
	t.Helper()
	j, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func appendRecord(t *testing.T, j *Journal, kind string, index int) Record {
	t.Helper()
	rec, err := j.Append(Record{
		Kind: kind,
		Source: Source{
			Namespace:   "codebuddy",
			EventID:     "evt-" + strconv.Itoa(index),
			RecordIndex: index,
		},
		Payload: json.RawMessage(`{"index":` + strconv.Itoa(index) + `}`),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return rec
}

func TestAppendAssignsStableSequenceAndPosition(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_stable"})

	var got []Record
	for i, kind := range []string{"run.phase", "turn.started", "turn.finished"} {
		got = append(got, appendRecord(t, j, kind, i))
	}
	if got[0].Seq != 1 || got[1].Seq != 2 || got[2].Seq != 3 {
		t.Fatalf("序号不是从 1 严格递增: %d %d %d", got[0].Seq, got[1].Seq, got[2].Seq)
	}
	data, err := os.ReadFile(filepath.Join(dir, JournalLog))
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var offset int64
	for i, rec := range got {
		if rec.Offset != offset {
			t.Fatalf("第 %d 条位置 = %d，期望 %d", i+1, rec.Offset, offset)
		}
		if rec.Line != i+1 {
			t.Fatalf("第 %d 条行号 = %d", i+1, rec.Line)
		}
		offset += int64(len(lines[i])) + 1
	}
	if got[0].DedupeKey != "codebuddy/evt-0" {
		t.Fatalf("厂商事件去重键 = %q", got[0].DedupeKey)
	}

	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openTestJournal(t, dir, Options{RunID: "run_stable"})
	recs := reopened.Records()
	if len(recs) != 3 {
		t.Fatalf("重开后记录数 = %d", len(recs))
	}
	for i := range got {
		if recs[i].Seq != got[i].Seq || recs[i].Offset != got[i].Offset || recs[i].DedupeKey != got[i].DedupeKey {
			t.Fatalf("重开后第 %d 条不稳定: %+v", i+1, recs[i])
		}
	}
}

func TestDedupeKeyFallsBackToRunAndSourcePosition(t *testing.T) {
	key := DeriveDedupeKey("run_x", Source{ParseVersion: "codebuddy/v1", Path: "stdout.log", RecordIndex: 7})
	if key != "run_x|codebuddy/v1|stdout.log|7" {
		t.Fatalf("回退去重键 = %q", key)
	}
	key = DeriveDedupeKey("run_x", Source{Namespace: "trae", EventID: "abc", ParseVersion: "v9", Path: "x", RecordIndex: 1})
	if key != "trae/abc" {
		t.Fatalf("厂商去重键 = %q", key)
	}
}

func TestRecoveryTruncatesTornTailOnly(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_torn"})
	appendRecord(t, j, "run.phase", 0)
	appendRecord(t, j, "turn.started", 1)
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(dir, JournalLog)
	partial := `{"seq":3,"offset":4096`
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("打开日志: %v", err)
	}
	if _, err := f.WriteString(partial); err != nil {
		t.Fatalf("写入撕裂尾部: %v", err)
	}
	_ = f.Close()

	reopened := openTestJournal(t, dir, Options{RunID: "run_torn"})
	if n := len(reopened.Records()); n != 2 {
		t.Fatalf("撕裂尾部不应成为记录，得到 %d 条", n)
	}
	st := reopened.Status()
	if st.TruncatedTailBytes != int64(len(partial)) {
		t.Fatalf("撕裂尾部字节 = %d，期望 %d", st.TruncatedTailBytes, len(partial))
	}
	if !st.Incomplete {
		t.Fatal("撕裂尾部必须在状态中显式可见")
	}
	rec := appendRecord(t, reopened, "run.phase", 2)
	if rec.Seq != 3 {
		t.Fatalf("恢复后序号 = %d，期望 3", rec.Seq)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if rec.Offset != info.Size()-int64(len(mustLine(t, rec))) {
		t.Fatalf("恢复后位置 %d 与文件末尾不一致", rec.Offset)
	}
}

func mustLine(t *testing.T, rec Record) []byte {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return append(data, '\n')
}

func TestCompleteLineWithoutNewlineIsRepaired(t *testing.T) {
	dir := t.TempDir()
	line := `{"seq":1,"offset":0,"line":1,"kind":"run.phase","dedupe_key":"codebuddy/evt-0","source":{"namespace":"codebuddy","event_id":"evt-0"},"received_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, JournalLog), []byte(line), 0o600); err != nil {
		t.Fatalf("写日志: %v", err)
	}
	j := openTestJournal(t, dir, Options{RunID: "run_repair"})
	if n := len(j.Records()); n != 1 {
		t.Fatalf("完整末行应保留，得到 %d 条", n)
	}
	rec := appendRecord(t, j, "run.phase", 1)
	if rec.Offset != int64(len(line))+1 {
		t.Fatalf("修复换行后下一条位置 = %d", rec.Offset)
	}
}

func TestCorruptMiddleLineIsRejected(t *testing.T) {
	dir := t.TempDir()
	good := `{"seq":1,"offset":0,"line":1,"kind":"run.phase","dedupe_key":"codebuddy/evt-0","source":{"namespace":"codebuddy","event_id":"evt-0"},"received_at":"2026-01-01T00:00:00Z"}` + "\n"
	bad := "not-json\n"
	if err := os.WriteFile(filepath.Join(dir, JournalLog), []byte(good+bad), 0o600); err != nil {
		t.Fatalf("写日志: %v", err)
	}
	if _, err := Open(dir, Options{RunID: "run_bad"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("中间坏行应拒绝，得到 %v", err)
	}
}

func TestCursorAdvancesOnlyForwardAndPersists(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_cursor"})
	for i := 0; i < 3; i++ {
		appendRecord(t, j, "turn.delta", i)
	}
	if err := j.Checkpoint(2); err != nil {
		t.Fatalf("Checkpoint(2): %v", err)
	}
	if pending := j.Pending(); len(pending) != 1 || pending[0].Seq != 3 {
		t.Fatalf("待交付 = %+v", pending)
	}
	if err := j.Checkpoint(1); !errors.Is(err, ErrCursor) {
		t.Fatalf("游标回退应拒绝，得到 %v", err)
	}
	if err := j.Checkpoint(9); !errors.Is(err, ErrCursor) {
		t.Fatalf("越界游标应拒绝，得到 %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openTestJournal(t, dir, Options{RunID: "run_cursor"})
	if reopened.Cursor().Seq != 2 {
		t.Fatalf("重开后游标 = %d", reopened.Cursor().Seq)
	}
}

func TestResubmitRetriesFromFailedRecord(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_resubmit"})
	for i := 0; i < 3; i++ {
		appendRecord(t, j, "turn.delta", i)
	}
	ctx := context.Background()
	failOnce := true
	var delivered []int64
	sub := SubmitterFunc(func(_ context.Context, rec Record) error {
		if rec.Seq == 2 && failOnce {
			failOnce = false
			return errors.New("database unavailable")
		}
		delivered = append(delivered, rec.Seq)
		return nil
	})

	rep, err := j.Resubmit(ctx, sub)
	if err == nil {
		t.Fatal("数据库失败应返回错误")
	}
	if rep.Delivered != 1 || j.Cursor().Seq != 1 {
		t.Fatalf("失败后 deliver=%d cursor=%d", rep.Delivered, j.Cursor().Seq)
	}
	rep, err = j.Resubmit(ctx, sub)
	if err != nil {
		t.Fatalf("第二次补交: %v", err)
	}
	if rep.Delivered != 2 || j.Cursor().Seq != 3 {
		t.Fatalf("第二次 deliver=%d cursor=%d", rep.Delivered, j.Cursor().Seq)
	}
	rep, err = j.Resubmit(ctx, sub)
	if err != nil || rep.Delivered != 0 {
		t.Fatalf("已补交不应重发: %+v %v", rep, err)
	}
	if len(delivered) != 3 {
		t.Fatalf("实际交付 %v", delivered)
	}
}

func TestResubmitTreatsAlreadyStoredAsDelivered(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_dup"})
	for i := 0; i < 2; i++ {
		appendRecord(t, j, "turn.delta", i)
	}
	sub := SubmitterFunc(func(_ context.Context, rec Record) error {
		if rec.Seq == 1 {
			return ErrAlreadyStored
		}
		return nil
	})
	rep, err := j.Resubmit(context.Background(), sub)
	if err != nil {
		t.Fatalf("补交: %v", err)
	}
	if rep.Duplicates != 1 || rep.Delivered != 1 || j.Cursor().Seq != 2 {
		t.Fatalf("重复补交结果 = %+v cursor=%d", rep, j.Cursor().Seq)
	}
}

func TestPruneKeepsUnsubmittedRecords(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_prune", MaxBytes: 400})
	payload := json.RawMessage(`{"text":"` + strings.Repeat("x", 120) + `"}`)
	for i := 0; i < 6; i++ {
		if _, err := j.Append(Record{
			Kind:    "turn.delta",
			Source:  Source{RecordIndex: i},
			Payload: payload,
		}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := j.Checkpoint(3); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rep, err := j.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if rep.Removed != 3 {
		t.Fatalf("应只删除 3 条已交付记录，实际 %d", rep.Removed)
	}
	recs := j.Records()
	if len(recs) != 3 {
		t.Fatalf("未交付记录被删除: 剩 %d 条", len(recs))
	}
	for _, rec := range recs {
		if rec.Seq < 4 || rec.Seq > 6 {
			t.Fatalf("保留下来的记录序号 = %d", rec.Seq)
		}
	}
	st := j.Status()
	if st.PrunedRecords != 3 || st.PendingRecords != 3 {
		t.Fatalf("状态 = %+v", st)
	}
	if !st.OverBudget {
		t.Fatal("未交付记录超出预算时必须显式可见")
	}
	if !st.Incomplete {
		t.Fatal("超出预算应标记不完整")
	}
}

func TestPruneRemovesAllDeliveredWhenBudgetAllows(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_prune_all", MaxBytes: 150})
	for i := 0; i < 2; i++ {
		appendRecord(t, j, "turn.delta", i)
	}
	if err := j.Checkpoint(2); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	rep, err := j.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if rep.Removed != 2 || len(j.Records()) != 0 {
		t.Fatalf("裁剪结果 = %+v，剩余 %d", rep, len(j.Records()))
	}
	if rep.OverBudget {
		t.Fatal("全部记录已删除时不应超出预算")
	}
	rec := appendRecord(t, j, "run.phase", 7)
	if rec.Seq != 3 || rec.Offset != 0 {
		t.Fatalf("裁剪后新记录 = %+v", rec)
	}
}

func TestRecordPayloadSecretsAreRedactedAndVisible(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_secret"})
	rec, err := j.Append(Record{
		Kind:    "turn.delta",
		Source:  Source{Namespace: "codebuddy", EventID: "evt-secret"},
		Payload: json.RawMessage(`{"note":"Authorization: Bearer abcdefghijkl","token":"abcdef123456"}`),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !rec.Redacted {
		t.Fatal("发生过脱敏的记录必须标记 Redacted")
	}
	data, err := os.ReadFile(filepath.Join(dir, JournalLog))
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	for _, secret := range []string{"abcdefghijkl", "abcdef123456"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("秘密 %q 落盘", secret)
		}
	}
	if !strings.Contains(string(data), Redacted) {
		t.Fatal("缺少脱敏占位")
	}
}

func TestAppendRejectsInvalidInput(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_invalid"})
	if _, err := j.Append(Record{}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("空 kind 应拒绝: %v", err)
	}
	if _, err := j.Append(Record{Kind: "x", Seq: 5}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("调用方指定序号应拒绝: %v", err)
	}
	if _, err := j.Append(Record{Kind: "x", Payload: json.RawMessage("plain text")}); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("非 JSON 负载应拒绝: %v", err)
	}
	now := time.Now().UTC()
	rec := appendRecord(t, j, "run.phase", 0)
	if rec.ReceivedAt.IsZero() || rec.ReceivedAt.After(now.Add(time.Minute)) {
		t.Fatalf("received_at = %v", rec.ReceivedAt)
	}
}
