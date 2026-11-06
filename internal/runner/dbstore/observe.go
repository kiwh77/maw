package dbstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// WriteFacts 把一次运行观测到的事实落库：来源会话、轮次、run 关联与用量。
//
// 缺什么就不写什么：没有来源会话身份就没有会话，没有 token 观测就没有用量记录
// （0 Token 冒充观测比没有观测更糟）。
//
// 四个动作合在一个事务里：会话、轮次、关联与用量要么都成立，要么都不成立——
// 只写一半会留下「有会话却没有轮次」这种看着像事实的残缺状态。
func (s *Store) WriteFacts(ctx context.Context, f observe.Facts) error {
	_ = ctx
	if s == nil || s.DB == nil {
		return nil
	}
	if strings.TrimSpace(f.SourceNS) == "" || strings.TrimSpace(f.SessionID) == "" {
		return nil
	}
	sessionID := observeSessionID(f)
	turnID := observeTurnID(f)
	projectID := f.ProjectID
	if projectID == "" {
		projectID = s.ProjectID
	}
	occurredAt := db.UTCNow()
	if !f.OccurredAt.IsZero() {
		occurredAt = f.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return s.DB.Write(func(tx *sql.Tx) error {
		if err := db.UpsertSourceSession(tx, db.SourceSession{
			SessionID:  sessionID,
			ProjectID:  projectID,
			SourceNS:   f.SourceNS,
			ExternalID: f.SessionID,
			Adapter:    f.SourceNS,
			OpenedAt:   occurredAt,
			Meta:       map[string]any{"source_session_id": f.SessionID},
		}); err != nil {
			return err
		}
		if !f.HasTurn() {
			// 只有会话身份：轮次与关联还没有事实依据，不硬凑。
			return nil
		}
		if err := db.UpsertSourceTurn(tx, db.SourceTurn{
			TurnID:     turnID,
			SourceNS:   f.SourceNS,
			SessionID:  sessionID,
			ExternalID: f.RecordUUID,
			StartedAt:  occurredAt,
			Meta:       map[string]any{"record_type": f.RecordType, "record_ref": f.RecordRef},
		}); err != nil {
			return err
		}
		if err := db.EnsureRunSourceLink(tx, db.RunSourceLink{
			LinkID:     observeLinkID(f, turnID),
			ProjectID:  projectID,
			RunID:      f.RunID,
			InstanceID: f.InstanceID,
			SessionID:  sessionID,
			TurnID:     turnID,
			Attributed: true,
		}); err != nil {
			return err
		}
		if !f.HasUsage() {
			// 没有 token 观测就不写用量记录。
			return nil
		}
		quality := f.Quality
		if !quality.Valid() {
			quality = "unknown"
		}
		return db.PutUsageRecord(tx, db.UsageRecord{
			UsageID:          observeUsageID(f, turnID),
			SourceNS:         f.SourceNS,
			TurnID:           turnID,
			Epoch:            1,
			Scope:            observeScope(f),
			Semantic:         db.UsageSemantic(f.Semantic),
			InputTokens:      f.InputTokens,
			OutputTokens:     f.OutputTokens,
			CacheReadTokens:  f.CacheReadTokens,
			CacheWriteTokens: f.CacheWriteTokens,
			TotalTokens:      f.TotalTokens,
			Quality:          quality,
			Basis:            f.Evidence,
		})
	})
}

// observeSessionID 是本库里的会话身份：来源命名空间 + 来源侧会话身份。
func observeSessionID(f observe.Facts) string {
	return f.SourceNS + ":" + f.SessionID
}

// observeTurnID 是本库里的轮次身份：来源会话 + 记录身份。
//
// 记录没有 uuid 时用记录位置的哈希兜底：同一个位置永远算出同一个轮次，
// 重放不会造出第二轮。
func observeTurnID(f observe.Facts) string {
	key := strings.TrimSpace(f.RecordUUID)
	if key == "" {
		key = shortHash(f.RecordRef)
	}
	return f.SourceNS + ":" + f.SessionID + ":" + key
}

// observeLinkID 是 run↔来源的关联身份：一次运行的一轮只有一个关联。
func observeLinkID(f observe.Facts, turnID string) string {
	return "link:" + f.RunID + ":" + turnID
}

// observeUsageID 是用量记账的身份：同一轮同一口径的同一代。
func observeUsageID(f observe.Facts, turnID string) string {
	return "usage:" + turnID + ":1:" + observeScope(f)
}

// observeScope 是用量的口径维度；没写就按一次运行记。
func observeScope(f observe.Facts) string {
	if s := strings.TrimSpace(f.Scope); s != "" {
		return s
	}
	return "run"
}

// shortHash 给没有自带身份的记录算一个稳定短标识。
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}
