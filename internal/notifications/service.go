package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

const (
	// 分页口径沿用 HTTP 契约 §2：省略为 50，允许 1–200，越界报错而不是静默截断。
	defaultPageLimit = 50
	maxPageLimit     = 200
	// maxDispatchBatch 是一次 Dispatch 最多处理的记录数。
	maxDispatchBatch = 200
	// maxRecoveryScan 是一次 Recover 最多扫描的未处理记录数；触顶时报告 Truncated。
	maxRecoveryScan = 1000
)

// AmbiguousDeliveryError 表示投递实现无法判断「用户是否已经看到」。
//
// 它必须被当作「可能重复」而不是普通失败：OS 弹窗调用超时、进程被信号打断、
// 托盘服务没回执，都属于这一类。把它们记成 failed 会让人以为「没弹过」，
// 下次重投就可能成为用户眼里的第二条。
type AmbiguousDeliveryError struct {
	Reason string
}

func (e *AmbiguousDeliveryError) Error() string {
	if strings.TrimSpace(e.Reason) == "" {
		return "notifications: 投递结果不可判定"
	}
	return "notifications: 投递结果不可判定：" + e.Reason
}

// Service 是提醒用例。零值不可用，请用 New 或显式构造。
type Service struct {
	// Now 可注入时钟（测试用）。默认 time.Now。
	Now func() time.Time
	// Policy 是提醒规则的可配置部分；零值取 DefaultPolicy。
	Policy Policy
	// Deliverers 按渠道登记投递实现。没有实现的渠道在 Dispatch 里被跳过
	// （站内渠道除外：记录本身就是提醒）。
	Deliverers map[Channel]Deliverer
}

// New 返回带默认策略的服务。
func New() *Service {
	return &Service{Policy: DefaultPolicy(), Deliverers: map[Channel]Deliverer{}}
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	return s.Policy.normalize()
}

func (s *Service) deliverer(ch Channel) Deliverer {
	if s == nil || s.Deliverers == nil {
		return nil
	}
	return s.Deliverers[ch]
}

// Raise 在调用方的事务内登记提醒。它不投递：投递必须在事务外（P132）。
//
// 返回每条渠道的结局：要么创建了一条提醒，要么给出压掉它的原因。
// 同一业务事件在同一渠道重放只会压一次 duplicate，不会产生第二条记录。
func (s *Service) Raise(tx *sql.Tx, ev BusinessEvent) (RaiseResult, error) {
	if errs := ValidateEvent(ev); len(errs) > 0 {
		return RaiseResult{}, model.Err(errs)
	}
	kind, needed := Classify(ev)
	if !needed {
		return RaiseResult{Skipped: []Skip{{Reason: SkipNotNeeded}}}, nil
	}
	pol := s.policy()
	channels := pol.Channels(kind)
	if len(channels) == 0 {
		return RaiseResult{Skipped: []Skip{{Reason: SkipNotConfigured}}}, nil
	}
	// 配置写错的渠道必须在登记时就报错：让它落库只会变成一条永远投不出去的
	// pending 记录，而「为什么没提醒」要等到有人翻库才发现。
	for _, ch := range channels {
		if !ch.Valid() {
			return RaiseResult{}, model.Violationf(model.CodePortFieldInvalid,
				"policy.rules."+string(kind), "未登记的渠道 %q", ch)
		}
	}

	now := s.now()
	// 已确认记录的去重窗口下界。窗口内刚确认过的同一状态算噪音；
	// 未确认的记录不受这个下界限制（见 recentByFingerprint）。
	since := stamp(now.Add(-pol.DedupWindow))
	fp := fingerprint(kind, ev.Subject, ev.State())

	var res RaiseResult
	for _, ch := range channels {
		// 1) 幂等键：同一业务事件同一渠道只提醒一次。
		existing, err := byEventChannel(tx, ev.EventID, ch)
		if err != nil {
			return RaiseResult{}, err
		}
		if existing != nil {
			res.Skipped = append(res.Skipped, Skip{
				Channel: ch, Reason: SkipDuplicate, NotificationID: existing.ID,
			})
			continue
		}

		// 2) 状态指纹：对象停在同一状态就别再打扰。
		prior, err := recentByFingerprint(tx, ev.ProjectID, ch, fp, since)
		if err != nil {
			return RaiseResult{}, err
		}
		if prior != nil {
			reason := SkipUnchanged
			if prior.Handled() {
				reason = SkipSettled
			}
			res.Skipped = append(res.Skipped, Skip{
				Channel: ch, Reason: reason, NotificationID: prior.ID,
			})
			continue
		}

		// 3) 新建一条 pending。
		id, err := newID()
		if err != nil {
			return RaiseResult{}, err
		}
		n := Notification{
			ID:        id,
			ProjectID: ev.ProjectID,
			EventID:   ev.EventID,
			Kind:      kind,
			Channel:   ch,
			Subject:   ev.Subject,
			State:     ev.State(),
			Reason:    ev.Reason,
			Title:     displayTitle(kind, ev.Subject, ev.Title),
			Body:      ev.Body,
			Status:    StatusPending,
			CreatedAt: stamp(now),
		}
		inserted, err := insert(tx, n, n.meta())
		if err != nil {
			return RaiseResult{}, err
		}
		if !inserted {
			// 另一个写入者先落了同一 (event_id, channel)：按重放处理，不覆盖它。
			winner, err := byEventChannel(tx, ev.EventID, ch)
			if err != nil {
				return RaiseResult{}, err
			}
			if winner == nil {
				return RaiseResult{}, fmt.Errorf(
					"notifications: 插入被忽略但按幂等键查不到记录（event=%s channel=%s）", ev.EventID, ch)
			}
			res.Skipped = append(res.Skipped, Skip{
				Channel: ch, Reason: SkipDuplicate, NotificationID: winner.ID,
			})
			continue
		}
		res.Created = append(res.Created, n)
	}
	return res, nil
}

// Unhandled 返回未处理的提醒：pending／delivered／failed 都算未处理，
// 只有 acked 才是处理完。按登记时间升序。
func (s *Service) Unhandled(tx *sql.Tx, projectID string, limit int) ([]Notification, error) {
	n, err := pageLimit(limit)
	if err != nil {
		return nil, err
	}
	return unhandled(tx, projectID, n)
}

// BeginAttempt 在事务内记下「即将投递」。
//
// 调用方必须在事务外执行投递，再用 CompleteAttempt 交回结果。
// 两者之间进程消失，记录会停在 OpenAttempt=true；重启后的 Recover 把它收成
// ambiguous——这正是「OS 弹窗不是原子投递」在账上的样子。
func (s *Service) BeginAttempt(tx *sql.Tx, notificationID string) (Attempt, error) {
	n, err := byID(tx, notificationID)
	if err != nil {
		return Attempt{}, err
	}
	if n == nil {
		return Attempt{}, ErrUnknownNotification
	}
	if n.Handled() {
		return Attempt{}, ErrAlreadyHandled
	}
	m := n.meta()
	m.Attempts++
	m.AttemptNo = m.Attempts
	m.AttemptStartedAt = stamp(s.now())
	// 上一次尝试还没收尾就再投一次：把它记成中断，而不是让两次尝试在账上合成一次。
	if n.OpenAttempt {
		m.AmbiguousAttempts++
		m.DuplicateRisk = true
	}
	m.AttemptOpen = true

	n.Attempts = m.Attempts
	n.AmbiguousAttempts = m.AmbiguousAttempts
	n.OpenAttempt = true
	n.DuplicateRisk = m.DuplicateRisk
	if err := update(tx, *n, m); err != nil {
		return Attempt{}, err
	}
	return Attempt{
		No: m.AttemptNo,
		Request: DeliveryRequest{
			NotificationID: n.ID,
			ProjectID:      n.ProjectID,
			Kind:           n.Kind,
			Channel:        n.Channel,
			Subject:        n.Subject,
			State:          n.State,
			Reason:         n.Reason,
			Title:          n.Title,
			Body:           n.Body,
			Attempt:        m.AttemptNo,
			DuplicateRisk:  m.DuplicateRisk,
		},
	}, nil
}

// CompleteAttempt 记下投递结果。attemptNo 必须与 BeginAttempt 返回的一致；
// 不一致说明另一次尝试改过这条记录，本次结果不能覆盖它（ErrAttemptMismatch）。
//
// 三种结局：
//
//   - 成功：status=delivered。注意这不等于用户看见，仍需 Ack。
//   - 失败：status=failed，保留未处理身份，下次可重投。
//   - 不可判定：status 留在未处理，AmbiguousAttempts+1、DuplicateRisk=true。
//     这是唯一诚实的写法——证据不足时不判成功也不判失败。
func (s *Service) CompleteAttempt(tx *sql.Tx, notificationID string, attemptNo int, out Outcome) (Notification, error) {
	n, err := byID(tx, notificationID)
	if err != nil {
		return Notification{}, err
	}
	if n == nil {
		return Notification{}, ErrUnknownNotification
	}
	m := n.meta()
	if n.Handled() {
		// 用户在投递途中确认了。Ack 已经收尾了那次尝试，本次结果作废：
		// 不报错（这不是调用方的错），也不把 acked 降回 delivered。
		return *n, nil
	}
	if !n.OpenAttempt || m.AttemptNo != attemptNo {
		return Notification{}, ErrAttemptMismatch
	}

	m.AttemptOpen = false
	m.AttemptStartedAt = ""
	now := stamp(s.now())
	switch {
	case out.Ambiguous:
		n.Status = StatusPending
		m.AmbiguousAttempts++
		m.DuplicateRisk = true
		m.LastError = out.Err
		if strings.TrimSpace(m.LastError) == "" {
			m.LastError = "投递结果不可判定"
		}
	case out.Delivered:
		n.Status = StatusDelivered
		n.DeliveredAt = now
		n.Evidence = out.Evidence
		m.Evidence = out.Evidence
		m.LastError = ""
	default:
		n.Status = StatusFailed
		m.LastError = out.Err
		if strings.TrimSpace(m.LastError) == "" {
			m.LastError = "投递失败"
		}
	}

	n.AmbiguousAttempts = m.AmbiguousAttempts
	n.DuplicateRisk = m.DuplicateRisk
	n.OpenAttempt = false
	n.LastError = m.LastError
	if err := update(tx, *n, m); err != nil {
		return Notification{}, err
	}
	return *n, nil
}

// Ack 记录用户确认。重复确认是幂等的：第二次返回 false 而不是报错。
//
// 确认会顺手收尾未完成的尝试：用户已经处理过这件事，那条在途投递的重复风险不再有意义。
func (s *Service) Ack(tx *sql.Tx, notificationID string, at time.Time) (bool, error) {
	n, err := byID(tx, notificationID)
	if err != nil {
		return false, err
	}
	if n == nil {
		return false, ErrUnknownNotification
	}
	if n.Handled() {
		return false, nil
	}
	if at.IsZero() {
		at = s.now()
	}
	n.Status = StatusAcked
	n.AckedAt = stamp(at)
	n.OpenAttempt = false
	m := n.meta()
	m.AttemptOpen = false
	m.AttemptStartedAt = ""
	m.LastError = ""
	n.LastError = ""
	if err := update(tx, *n, m); err != nil {
		return false, err
	}
	return true, nil
}

// Recover 在启动时调用：把中断的投递收尾，并列出可恢复的提醒。
//
// 它只改「未完成尝试」这一个标记，不重投、不改确认状态：重投是 Dispatch 的事，
// 这样「重启后发生了什么」与「又弹了一次」是两件可分开观察的事。
func (s *Service) Recover(tx *sql.Tx, projectID string) (RecoveryReport, error) {
	rows, err := unhandled(tx, projectID, maxRecoveryScan)
	if err != nil {
		return RecoveryReport{}, err
	}
	rep := RecoveryReport{Scanned: len(rows), Truncated: len(rows) >= maxRecoveryScan}
	for _, n := range rows {
		if n.OpenAttempt {
			m := n.meta()
			m.AttemptOpen = false
			m.AttemptStartedAt = ""
			m.AmbiguousAttempts++
			m.DuplicateRisk = true
			m.LastError = "投递中断：意图已记、结果未记，可能已经弹过"
			n.OpenAttempt = false
			n.AmbiguousAttempts = m.AmbiguousAttempts
			n.DuplicateRisk = true
			n.LastError = m.LastError
			if err := update(tx, n, m); err != nil {
				return rep, err
			}
			rep.Ambiguous = append(rep.Ambiguous, n)
			continue
		}
		switch n.Status {
		case StatusPending, StatusFailed:
			rep.Resumable = append(rep.Resumable, n)
		case StatusDelivered:
			rep.AwaitingAck = append(rep.AwaitingAck, n)
		}
	}
	return rep, nil
}

// Prune 执行保留策略：只删已确认且确认时间早于保留窗口的记录。
//
// 未处理的提醒（pending／delivered／failed，含 ambiguous）永不因保留策略消失——
// 「提醒被清理策略吃掉」比「提醒多留了几天」严重得多。projectID 为空表示全部工程。
func (s *Service) Prune(tx *sql.Tx, projectID string) (PruneReport, error) {
	cutoff := stamp(s.now().Add(-s.policy().RetentionAcked))
	removed, err := deleteAckedBefore(tx, projectID, cutoff)
	if err != nil {
		return PruneReport{}, err
	}
	kept, err := countRows(tx, projectID)
	if err != nil {
		return PruneReport{}, err
	}
	return PruneReport{Cutoff: cutoff, Removed: removed, Kept: kept}, nil
}

// Dispatch 把未处理的提醒投递出去。
//
// 事务切分是这里唯一重要的事：每次投递用两个短事务（记意图／记结果），
// 中间的外部调用（OS 弹窗）不持有任何事务，也不在事务里做网络或进程动作（P132）。
// 两个事务之间的崩溃由下一次启动的 Recover 收尾。
func (s *Service) Dispatch(ctx context.Context, store TxRunner, projectID string) (DispatchReport, error) {
	if store == nil {
		return DispatchReport{}, model.Violation(model.CodePortFieldInvalid, "store", "事务入口必填")
	}
	var batch []Notification
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		batch, err = deliverable(tx, projectID, maxDispatchBatch)
		return err
	}); err != nil {
		return DispatchReport{}, err
	}

	var rep DispatchReport
	for _, n := range batch {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		d := s.deliverer(n.Channel)
		if d == nil && n.Channel != ChannelInApp {
			// 没有投递实现：留在 pending，等接入层补上，不算失败。
			rep.Skipped++
			continue
		}

		var att Attempt
		err := store.Write(func(tx *sql.Tx) error {
			var e error
			att, e = s.BeginAttempt(tx, n.ID)
			return e
		})
		if errors.Is(err, ErrAlreadyHandled) {
			rep.Handled++
			continue
		}
		if errors.Is(err, ErrUnknownNotification) {
			continue
		}
		if err != nil {
			return rep, err
		}

		out := s.invoke(ctx, d, att.Request)

		err = store.Write(func(tx *sql.Tx) error {
			_, e := s.CompleteAttempt(tx, n.ID, att.No, out)
			return e
		})
		if errors.Is(err, ErrAttemptMismatch) || errors.Is(err, ErrUnknownNotification) {
			// 记录被别处改过（用户确认、另一次投递）：本次结果作废，但不算错误。
			continue
		}
		if err != nil {
			return rep, err
		}
		switch {
		case out.Ambiguous:
			rep.Ambiguous++
		case out.Delivered:
			rep.Delivered++
		default:
			rep.Failed++
		}
	}
	return rep, nil
}

// invoke 执行一次外部投递。它必须在事务外被调用。
func (s *Service) invoke(ctx context.Context, d Deliverer, req DeliveryRequest) Outcome {
	if d == nil {
		// 站内渠道：记录本身就是提醒，投递即成立，证据是这条记录。
		return Outcome{Delivered: true, Evidence: "in_app:record"}
	}
	receipt, err := d.Deliver(ctx, req)
	if err != nil {
		var ambiguous *AmbiguousDeliveryError
		if errors.As(err, &ambiguous) {
			return Outcome{Ambiguous: true, Err: ambiguous.Error()}
		}
		return Outcome{Delivered: false, Err: err.Error()}
	}
	return Outcome{Delivered: true, Evidence: receipt.Evidence}
}

// displayTitle 在调用方没给文案时兜底。真正的文案属于界面层（i18n），
// 本包只保证「有东西可显示」，不假装自己是文案库。
func displayTitle(kind Kind, subject, title string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return string(kind) + " " + subject
}

func pageLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return defaultPageLimit, nil
	case limit < 1 || limit > maxPageLimit:
		return 0, model.Violationf(model.CodePortFieldInvalid, "limit",
			"允许 1–%d，收到 %d", maxPageLimit, limit)
	}
	return limit, nil
}
