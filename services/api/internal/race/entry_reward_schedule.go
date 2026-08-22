// 參賽虛擬獎勵（entry reward，migration 140）：賽事開始後自動發放給所有「已報名(paid)」者，不看任何
// 任務/完成條件、人人有獎（例：9折序號）。沿用即時獎勵設定結構與抽獎/發放引擎
// （activityreward.RewardConfig/RollAndGrant，見 races.entry_reward_config），比照 progress.go
// MarkRaceTaskCompletedAndGrant「CAS INSERT + 同交易 roll」的冪等範本——差異只在冪等表改用
// race_entry_reward_grants（UNIQUE(race_id,user_id)：每人每賽事最多發一次，不是每任務）。
//
// 純排程掃描（不掛報名 hook，簡潔可靠）：每 tick 撈「已開賽（start_date<=NOW()）且結束未超過寬限期
// （end_date+entryRewardGraceDays 天內）且設定了 entry_reward_config」的賽事 → 對每場撈「paid 報名者中
// 尚未在 grants 表的人」（LEFT JOIN 反查）→ 逐人 CAS 搶＋roll 發放 → 有實際發出東西才寄站內信。
//   - 開賽後才報名的人：下個 tick（5 分鐘）自動撿到，最多延遲 5 分鐘。
//   - 取消報名者：已發不追回（比照「已發 EXP 不追回」既有慣例——一旦 CAS 成功插入 grants，之後即使
//     報名被取消也不會撤銷已發放的獎勵）；取消後才輪到的候選（非 paid）本來就不會被撈到，不會誤發。
//   - 單人失敗：recover+log 隔離，不影響同批其餘候選；grants 未寫入，下個 tick 自動重試。
//   - roll 全槓龜（沒中任何項）也視為「已處理」，grants 照樣寫入、不會下次重 roll——人人有獎場景管理者
//     自己會把機率設 100%，槓龜只會發生在管理者刻意設 <100% 的情境，此時「沒中」本身就是合法結果。
package race

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/activityreward"
)

const (
	// entryRewardTickInterval 排程檢查間隔。比照 payment.vip_renewal.go 的取捨：不做「近期有人活動才查
	// DB」的閒置省睡眠優化——候選查詢本身有索引、結果集小（僅「已開賽且設定了 entry_reward_config」的
	// 賽事 × 各自「尚未發放的 paid 報名者」），5 分鐘一次對 Neon 負擔可忽略，換取「開賽後才報名者」與
	// 「賽事剛開始那刻」都能儘快被處理到的時效性。
	entryRewardTickInterval = 5 * time.Minute

	// entryRewardAdvisoryLockName pg_try_advisory_lock 用的鎖名（經 hashtext 轉成 lock id）。純粹是效率
	// 考量、減少多實例情境下的重複查詢，真正的冪等防線是 race_entry_reward_grants 的
	// UNIQUE(race_id,user_id) CAS（比照 payment.vip_renewal.go runRenewalBatch 檔頭註解的設計取捨）。
	entryRewardAdvisoryLockName = "race_entry_reward_scan"

	// entryRewardCandidateRaceLimit 單次 tick 最多掃描的賽事數；正常情境「同時開賽中且設定了參賽虛擬
	// 獎勵」的賽事數遠小於此值，此為防禦性上限。
	entryRewardCandidateRaceLimit = 200

	// entryRewardBatchPerRace 單場賽事單次 tick 最多處理的待發放人數；剩餘的人下個 tick 繼續撿。
	entryRewardBatchPerRace = 200

	// entryRewardGraceDays 賽事結束後仍繼續掃描發放的寬限天數：覆蓋「開賽後、賽事結束前才報名」的所有
	// 人（最晚在 end_date 之前都可能報名成功並轉 paid），略留緩衝避免邊界剛好被寬限窗切掉。超過寬限期
	// 後即使該賽事還有理論上未發放的 paid 報名者（正常不該發生），也不再繼續無限期掃描下去。
	entryRewardGraceDays = 7
)

// RunEntryRewardLoop 背景排程：比照 event.Handler.RunScheduleLoop／payment.BindHandler.RunRenewalLoop
// 的迴圈骨架，啟動時先跑一次（補停機期間應處理的候選），之後每 entryRewardTickInterval 跑一次；ctx
// 取消即結束。
func (s *Service) RunEntryRewardLoop(ctx context.Context) {
	s.runEntryRewardBatch(ctx)
	t := time.NewTicker(entryRewardTickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.runEntryRewardBatch(ctx)
		}
	}
}

// runEntryRewardBatch 取得批次互斥鎖（見檔頭註解：pg_try_advisory_lock + 專屬連線持有到批次結束才手動
// unlock+release，比照 payment.vip_renewal.go runRenewalBatch 的寫法）→ 撈候選賽事 → 逐場處理。
func (s *Service) runEntryRewardBatch(ctx context.Context) {
	conn, err := s.repo.db.Acquire(ctx)
	if err != nil {
		log.Error().Err(err).Msg("entry reward: acquire dedicated connection for advisory lock failed")
		return
	}
	defer conn.Release()

	var gotLock bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, entryRewardAdvisoryLockName).Scan(&gotLock); err != nil {
		log.Error().Err(err).Msg("entry reward: try advisory lock failed")
		return
	}
	if !gotLock {
		log.Debug().Msg("entry reward: another instance is already running this tick's batch, skip")
		return
	}
	defer func() {
		var unlocked bool
		if err := conn.QueryRow(ctx, `SELECT pg_advisory_unlock(hashtext($1))`, entryRewardAdvisoryLockName).Scan(&unlocked); err != nil {
			log.Warn().Err(err).Msg("entry reward: advisory unlock failed (will auto-release once this connection closes)")
		}
	}()

	races, err := s.repo.ListEntryRewardActiveRaces(ctx, entryRewardCandidateRaceLimit, entryRewardGraceDays)
	if err != nil {
		log.Error().Err(err).Msg("entry reward: load candidate races failed")
		return
	}
	for _, rc := range races {
		s.processEntryRewardRaceSafe(ctx, rc)
	}
}

// entryRewardRace 一場候選賽事（已開賽、在寬限窗內、設定了 entry_reward_config）。
type entryRewardRace struct {
	ID     string
	Title  string
	Config *activityreward.RewardConfig
}

// processEntryRewardRaceSafe panic recover + 逾時保護，確保單一賽事出狀況不會拖垮整批（比照
// payment.vip_renewal.go processRenewalCandidateSafe 的單筆隔離慣例）。
func (s *Service) processEntryRewardRaceSafe(ctx context.Context, rc entryRewardRace) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("race_id", rc.ID).Msg("entry reward: panic recovered while processing race")
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := s.processEntryRewardRace(cctx, rc); err != nil {
		log.Error().Err(err).Str("race_id", rc.ID).Msg("entry reward: process race failed")
	}
}

// processEntryRewardRace 撈這場賽事「paid 報名者中尚未發放」的候選（單批上限
// entryRewardBatchPerRace，剩餘的下個 tick 繼續撿），逐人 CAS 搶＋roll 發放。
func (s *Service) processEntryRewardRace(ctx context.Context, rc entryRewardRace) error {
	userIDs, err := s.repo.ListUnrewardedPaidRegistrants(ctx, rc.ID, entryRewardBatchPerRace)
	if err != nil {
		return fmt.Errorf("list unrewarded paid registrants: %w", err)
	}
	for _, userID := range userIDs {
		s.grantEntryRewardSafe(ctx, rc, userID)
	}
	return nil
}

// grantEntryRewardSafe 單人 CAS 搶＋roll 發放，panic recover 隔離；有實際發出東西才寄站內信
// （h.mail 未注入時直接跳過，不影響發獎本身——比照 payment.BindHandler.sendRenewalMail 的取捨）。
func (s *Service) grantEntryRewardSafe(ctx context.Context, rc entryRewardRace, userID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().Interface("panic", r).Str("race_id", rc.ID).Str("user_id", userID).
				Msg("entry reward: panic recovered while granting")
		}
	}()
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	claimed, granted, err := s.repo.GrantEntryRewardCAS(cctx, rc.ID, rc.Title, userID, rc.Config)
	if err != nil {
		log.Error().Err(err).Str("race_id", rc.ID).Str("user_id", userID).Msg("entry reward: grant failed")
		return
	}
	if !claimed || len(granted) == 0 || s.mail == nil {
		return
	}
	title, body := entryRewardMail(rc.Title)
	if _, mErr := s.mail.InsertForUsers(cctx, []string{userID}, "normal", title, body, ""); mErr != nil {
		log.Warn().Err(mErr).Str("race_id", rc.ID).Str("user_id", userID).Msg("entry reward: send mail failed")
	}
}

// entryRewardMail 通知文案（純函式，方便單元測試，比照 payment.vip_renewal.go 的文案函式慣例）。
func entryRewardMail(raceTitle string) (title, body string) {
	return "🎁 參賽獎勵已發放", fmt.Sprintf("恭喜！您在「%s」的參賽虛擬獎勵已發放，前往「活動獎勵」查看內容。", raceTitle)
}

// isEntryRewardWindowActive 純函式：賽事是否落在「應該被排程掃描」的視窗內（已開賽、結束未超過寬限期）
// ——鏡射 ListEntryRewardActiveRaces 的 SQL WHERE 條件(start_date<=now AND end_date>=now-graceDays天)，
// 抽成純 Go 函式讓時間邊界語意可離線單元測試，不需要真的連 DB（本套件沒有 DB 整合測試地基，比照
// model.go MinGroupFeeCents／payment/vip_renewal.go classifyQueryTradeResult 等既有的「SQL 邏輯鏡射成
// 純函式供單測」慣例）。
func isEntryRewardWindowActive(now, startDate, endDate time.Time, graceDays int) bool {
	if now.Before(startDate) {
		return false
	}
	graceCutoff := endDate.AddDate(0, 0, graceDays)
	return !now.After(graceCutoff)
}

// --- Repository：候選查詢 + CAS 發放（DB 讀寫本身沿用既有慣例，見上方個別函式註解） ---

// ListEntryRewardActiveRaces 撈「已開賽（start_date<=NOW()）且結束未超過寬限期（見
// isEntryRewardWindowActive 純函式鏡射同一條件）且設定了參賽虛擬獎勵(entry_reward_config 非空)」的賽事，
// 供 runEntryRewardBatch 逐場掃描待發放的 paid 報名者。
func (r *Repository) ListEntryRewardActiveRaces(ctx context.Context, limit, graceDays int) ([]entryRewardRace, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id::text, title, entry_reward_config
		FROM races
		WHERE entry_reward_config IS NOT NULL
		  AND start_date <= NOW()
		  AND end_date >= NOW() - make_interval(days => $2)
		ORDER BY start_date
		LIMIT $1`, limit, graceDays)
	if err != nil {
		return nil, fmt.Errorf("query candidate races: %w", err)
	}
	defer rows.Close()

	var out []entryRewardRace
	for rows.Next() {
		var rc entryRewardRace
		var cfgBytes []byte
		if err := rows.Scan(&rc.ID, &rc.Title, &cfgBytes); err != nil {
			return nil, err
		}
		cfg, err := bytesToRewardConfig(cfgBytes)
		if err != nil {
			return nil, fmt.Errorf("parse entry_reward_config (race %s): %w", rc.ID, err)
		}
		if cfg == nil || len(cfg.Items) == 0 {
			continue // 防禦性：理論上已被 WHERE entry_reward_config IS NOT NULL 排除，仍保底跳過空設定
		}
		rc.Config = cfg
		out = append(out, rc)
	}
	return out, rows.Err()
}

// ListUnrewardedPaidRegistrants 撈某賽事「paid 報名者中尚未在 race_entry_reward_grants 的人」
// （LEFT JOIN 反查是否已發過，標準的 anti-join 寫法：JOIN 條件放 ON、用 WHERE 右表 id IS NULL 篩出
// 「未曾成功配對」的列）。取消報名者(status<>'paid')天然不在候選之列，不會誤發；已取消但曾經 paid
// 過的人若已在 grants 表也不會被重複列出（已發不追回，見檔頭註解）。
func (r *Repository) ListUnrewardedPaidRegistrants(ctx context.Context, raceID string, limit int) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT reg.user_id::text
		FROM registrations reg
		LEFT JOIN race_entry_reward_grants g ON g.race_id = reg.race_id AND g.user_id = reg.user_id
		WHERE reg.race_id = $1 AND reg.status = 'paid' AND g.id IS NULL
		LIMIT $2`, raceID, limit)
	if err != nil {
		return nil, fmt.Errorf("query unrewarded paid registrants: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}

// GrantEntryRewardCAS CAS 標記「這位使用者這場賽事」的參賽虛擬獎勵已發放：INSERT
// race_entry_reward_grants ON CONFLICT(race_id,user_id) DO NOTHING，只有真的插入新列
// （RowsAffected==1，代表這次呼叫是「第一個」把這個使用者這場賽事標記已發放的）才在同一交易內 roll 發放
// （比照 progress.go MarkRaceTaskCompletedAndGrant 的「CAS INSERT + 同交易 roll」範本；唯一差異：這裡是
// 每人每賽事最多一次，不是每任務）。任一步失敗，交易整個 Rollback——grant 不落地，下個 tick 會再次嘗試，
// 不會出現「已標記發放但獎沒發」的半成功。roll 全槓龜（沒中任何項）也視為「已處理」，grants 已寫入不會
// 下次重 roll。
func (r *Repository) GrantEntryRewardCAS(ctx context.Context, raceID, raceTitle, userID string, cfg *activityreward.RewardConfig) (claimed bool, granted []activityreward.GrantedReward, err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	tag, err := tx.Exec(ctx, `
		INSERT INTO race_entry_reward_grants (race_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (race_id, user_id) DO NOTHING`, raceID, userID)
	if err != nil {
		return false, nil, fmt.Errorf("insert entry reward grant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil, nil // 沒搶到（此使用者此賽事先前已發放過，或被同一 tick 的另一實例搶先）：no-op
	}

	var issuedGroupIDs []string
	if cfg != nil && len(cfg.Items) > 0 {
		if granted, issuedGroupIDs, err = activityreward.RollAndGrant(ctx, tx, userID, "race_entry", raceID, "", cfg); err != nil {
			return false, nil, fmt.Errorf("roll and grant: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, nil, fmt.Errorf("commit: %w", err)
	}
	// commit 後才用非 tx 連線查真實庫存告警，理由與 MarkRaceTaskCompletedAndGrant／
	// MarkAttemptCompletedAndGrant 完全相同（見該二函式註解：READ COMMITTED 下 tx 內反推會導致漏報/洗頻）。
	if len(issuedGroupIDs) > 0 {
		go checkAndNotifyLowStock(r.db, raceTitle, issuedGroupIDs)
	}
	return true, granted, nil
}
