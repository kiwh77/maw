package notifications

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 表名与列名照抄 internal/db/migrations.go 的 notification_deliveries。
// 本包不建表、不改 schema；改这里必须同时改迁移，否则测试会直接报列不存在。
const (
	tableNotifications = "notification_deliveries"
	// stampLayout 与 internal/db.UTCNow 同口径：定长、UTC、秒级。
	// 定长且带 Z 的串按字典序比较就等于按时间比较，去重窗口与保留策略都靠这一点。
	stampLayout = "2006-01-02T15:04:05Z"

	selectColumns = `notification_id, project_id, event_id, channel, status, created_at,
  COALESCE(delivered_at, ''), COALESCE(acked_at, ''), meta_json`
)

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

// meta 是塞进 meta_json 的全部附加状态。
//
// 为什么不加列：表结构属 internal/db 的迁移（本卡 write_scope 不含它），
// 而「指纹、尝试次数、中断次数」是本包自己的语义，正好落在 schema 给的 meta_json 里。
type meta struct {
	Kind              string `json:"kind"`
	Subject           string `json:"subject"`
	State             string `json:"state"`
	Reason            string `json:"reason,omitempty"`
	Fingerprint       string `json:"fingerprint"`
	Title             string `json:"title,omitempty"`
	Body              string `json:"body,omitempty"`
	Attempts          int    `json:"attempts"`
	AmbiguousAttempts int    `json:"ambiguous_attempts"`
	AttemptOpen       bool   `json:"attempt_open"`
	AttemptNo         int    `json:"attempt_no,omitempty"`
	AttemptStartedAt  string `json:"attempt_started_at,omitempty"`
	DuplicateRisk     bool   `json:"duplicate_risk"`
	Evidence          string `json:"evidence,omitempty"`
	LastError         string `json:"last_error,omitempty"`
}

func (m meta) encode() string {
	raw, err := json.Marshal(m)
	if err != nil {
		// 结构体只含字符串与整数，编码失败说明代码被改坏，不该静默吞掉。
		panic("notifications: meta 编码失败: " + err.Error())
	}
	return string(raw)
}

func decodeMeta(raw string) meta {
	var m meta
	if strings.TrimSpace(raw) == "" {
		return m
	}
	// 解析失败不 panic：历史行可能来自更早的写法。此时字段取零值，
	// 指纹为空意味着「无法判重」，只会多提醒一次，不会吞掉提醒。
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}

// toNotification 把一行拼成对外结构。
func toNotification(id, projectID, eventID, channel, status, createdAt, deliveredAt, ackedAt, rawMeta string) Notification {
	m := decodeMeta(rawMeta)
	return Notification{
		ID:                id,
		ProjectID:         projectID,
		EventID:           eventID,
		Kind:              Kind(m.Kind),
		Channel:           Channel(channel),
		Subject:           m.Subject,
		State:             m.State,
		Reason:            m.Reason,
		Title:             m.Title,
		Body:              m.Body,
		Status:            Status(status),
		CreatedAt:         createdAt,
		DeliveredAt:       deliveredAt,
		AckedAt:           ackedAt,
		Attempts:          m.Attempts,
		AmbiguousAttempts: m.AmbiguousAttempts,
		OpenAttempt:       m.AttemptOpen,
		DuplicateRisk:     m.DuplicateRisk,
		Evidence:          m.Evidence,
		LastError:         m.LastError,
		attemptNo:         m.AttemptNo,
		attemptStartedAt:  m.AttemptStartedAt,
	}
}

func (n Notification) meta() meta {
	return meta{
		Kind:              string(n.Kind),
		Subject:           n.Subject,
		State:             n.State,
		Reason:            n.Reason,
		Fingerprint:       fingerprint(n.Kind, n.Subject, n.State),
		Title:             n.Title,
		Body:              n.Body,
		Attempts:          n.Attempts,
		AmbiguousAttempts: n.AmbiguousAttempts,
		AttemptOpen:       n.OpenAttempt,
		AttemptNo:         n.attemptNo,
		AttemptStartedAt:  n.attemptStartedAt,
		DuplicateRisk:     n.DuplicateRisk,
		Evidence:          n.Evidence,
		LastError:         n.LastError,
	}
}

func scanRow(row interface{ Scan(dest ...any) error }) (Notification, error) {
	var id, projectID, eventID, channel, status, createdAt, deliveredAt, ackedAt, rawMeta string
	if err := row.Scan(&id, &projectID, &eventID, &channel, &status, &createdAt,
		&deliveredAt, &ackedAt, &rawMeta); err != nil {
		return Notification{}, err
	}
	return toNotification(id, projectID, eventID, channel, status, createdAt,
		deliveredAt, ackedAt, rawMeta), nil
}

// newID 生成提醒身份。随机而不是指纹派生：同一对象同一状态在去重窗口之后
// 可能合法地再提醒一次，那时它必须是一条新记录，不能覆盖旧的身份。
func newID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("notifications: 生成身份失败: %w", err)
	}
	return "ntf_" + hex.EncodeToString(buf[:]), nil
}

// insert 写入一条提醒。返回 false 表示 (event_id, channel) 已存在（重放）。
func insert(tx *sql.Tx, n Notification, m meta) (bool, error) {
	res, err := tx.Exec(`INSERT INTO `+tableNotifications+`(
  notification_id, project_id, event_id, channel, status, created_at, delivered_at, acked_at, meta_json)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT DO NOTHING`,
		n.ID, n.ProjectID, n.EventID, string(n.Channel), string(n.Status), n.CreatedAt,
		nullStr(n.DeliveredAt), nullStr(n.AckedAt), m.encode())
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// byID 按身份读取；不存在返回 (nil, nil)。
func byID(tx *sql.Tx, id string) (*Notification, error) {
	row := tx.QueryRow(`SELECT `+selectColumns+` FROM `+tableNotifications+`
WHERE notification_id = ?`, id)
	n, err := scanRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// byEventChannel 按幂等键读取；不存在返回 (nil, nil)。
func byEventChannel(tx *sql.Tx, eventID string, channel Channel) (*Notification, error) {
	row := tx.QueryRow(`SELECT `+selectColumns+` FROM `+tableNotifications+`
WHERE event_id = ? AND channel = ?`, eventID, string(channel))
	n, err := scanRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// recentByFingerprint 找同渠道同指纹的最近一条，用于「状态没变就别再打扰」。
//
// 两个条件缺一不可：
//
//   - 未确认的记录不受时间限制地参与压制：用户还没处理，再弹一次只是噪音；
//   - 已确认的记录只在去重窗口内参与压制，且窗口从「确认时间」起算而不是登记时间：
//     用户 10 点才处理掉 9 点那条提醒，10 点 05 分同一状态再来显然还是噪音，
//     按登记时间算窗口会提前失效。窗口外再次发生则是新的一次介入需求。
//
// 这里用 json_extract 下推到 SQL，而不是把整张表取回内存再筛：
// 后者在提醒量大时会把每次 Raise 变成全表扫描。
func recentByFingerprint(tx *sql.Tx, projectID string, channel Channel, fp, since string) (*Notification, error) {
	row := tx.QueryRow(`SELECT `+selectColumns+` FROM `+tableNotifications+`
WHERE project_id = ? AND channel = ?
  AND (status <> 'acked' OR acked_at >= ?)
  AND json_extract(meta_json, '$.fingerprint') = ?
ORDER BY created_at DESC, notification_id DESC
LIMIT 1`, projectID, string(channel), since, fp)
	n, err := scanRow(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &n, nil
}

// unhandled 列出未处理记录（pending／delivered／failed），按登记时间升序。
//
// 「未处理」是给人看的：delivered 只说明弹窗调用成功了，用户还没确认，
// 所以它必须留在列表里。
func unhandled(tx *sql.Tx, projectID string, limit int) ([]Notification, error) {
	return queryList(tx, `status <> 'acked'`, projectID, limit)
}

// deliverable 列出还需要投递的记录：pending（从没投成功）与 failed（投失败）。
//
// 不含 delivered：已经投递成功、只是在等用户确认的记录再弹一次就是纯噪音。
// 「未处理」与「待投递」是两个集合，混用会让每次轮询都重复弹窗。
func deliverable(tx *sql.Tx, projectID string, limit int) ([]Notification, error) {
	return queryList(tx, `status IN ('pending', 'failed')`, projectID, limit)
}

func queryList(tx *sql.Tx, where, projectID string, limit int) ([]Notification, error) {
	rows, err := tx.Query(`SELECT `+selectColumns+` FROM `+tableNotifications+`
WHERE `+where+` AND (? = '' OR project_id = ?)
ORDER BY created_at ASC, notification_id ASC
LIMIT ?`, projectID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// update 整体重写一条记录的可变列。调用方必须在同一事务里先读过它，
// 否则 meta 里的尝试计数会被旧值覆盖。
func update(tx *sql.Tx, n Notification, m meta) error {
	_, err := tx.Exec(`UPDATE `+tableNotifications+`
SET status = ?, delivered_at = ?, acked_at = ?, meta_json = ?
WHERE notification_id = ?`,
		string(n.Status), nullStr(n.DeliveredAt), nullStr(n.AckedAt), m.encode(), n.ID)
	return err
}

// deleteAckedBefore 删除确认时间早于 cutoff 的已确认记录。
func deleteAckedBefore(tx *sql.Tx, projectID, cutoff string) (int64, error) {
	res, err := tx.Exec(`DELETE FROM `+tableNotifications+`
WHERE status = 'acked' AND acked_at IS NOT NULL AND acked_at < ?
  AND (? = '' OR project_id = ?)`, cutoff, projectID, projectID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// countRows 统计范围内记录数，供保留策略报告「还剩多少」。
func countRows(tx *sql.Tx, projectID string) (int, error) {
	row := tx.QueryRow(`SELECT COUNT(*) FROM `+tableNotifications+`
WHERE (? = '' OR project_id = ?)`, projectID, projectID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func nullStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
