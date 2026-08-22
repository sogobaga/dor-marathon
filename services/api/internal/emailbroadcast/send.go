package emailbroadcast

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/dor/api/internal/notify"
)

const (
	// broadcastBatchSize Resend /emails/batch 單次上限 100 封。
	broadcastBatchSize = 100

	// broadcastBatchSleep 批次間隔。Resend 預設 rate limit 約 2 req/s（每批已是一次 request），
	// 600ms ≈ 1.67 req/s，留有餘裕不誤觸 429。
	broadcastBatchSleep = 600 * time.Millisecond

	// broadcastSendTimeout 單批 Resend API 呼叫的逾時（背景 goroutine，不受 HTTP 請求生命週期影響，
	// 用獨立 context.Background 另包 timeout，比照 notify.Telegram 呼叫端慣例）。
	broadcastSendTimeout = 30 * time.Second
)

// runBroadcastSafe panic recover 包裝，確保背景寄送過程萬一 panic 也不會拖垮整個 process，並把
// email_broadcasts 對應列標記為 failed（比照 race.processEntryRewardRaceSafe 的單筆隔離慣例）。
func (h *Handler) runBroadcastSafe(id, subject, bodyHTML string, recipients []recipient) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Error().Interface("panic", rec).Str("broadcast_id", id).Msg("email broadcast: panic recovered")
			h.finishBroadcast(id, 0, len(recipients), fmt.Sprintf("內部錯誤：%v", rec))
		}
	}()
	h.runBroadcast(id, subject, bodyHTML, recipients)
}

// runBroadcast 逐批（每批 broadcastBatchSize 人）呼叫 notify.SendEmailBatch，批次間 sleep 節流；
// 遇到「服務未設定」或「額度用盡」立即停止（後續批次必然是同樣結果，繼續打只是浪費並洗爆
// error log），其餘錯誤視為該批全部失敗但繼續嘗試下一批（例如單次網路抖動不該讓整個廣播中止）。
func (h *Handler) runBroadcast(id, subject, bodyHTML string, recipients []recipient) {
	from := notify.DefaultFrom()
	sent, failed := 0, 0
	var lastErrNote string

batchLoop:
	for i := 0; i < len(recipients); i += broadcastBatchSize {
		end := min(i+broadcastBatchSize, len(recipients))
		batch := recipients[i:end]

		msgs := make([]notify.EmailMsg, len(batch))
		for j, rc := range batch {
			msgs[j] = notify.EmailMsg{
				To:      []string{rc.Email},
				Subject: subject,
				HTML:    buildEmailHTML(subject, bodyHTML, h.unsubscribeURL(rc.ID)),
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), broadcastSendTimeout)
		err := notify.SendEmailBatch(ctx, from, msgs)
		cancel()

		switch {
		case err == nil:
			sent += len(batch)

		case errors.Is(err, notify.ErrEmailNotConfigured):
			failed += len(recipients) - i
			lastErrNote = "Email 服務未設定"
			log.Error().Str("broadcast_id", id).Msg("email broadcast: RESEND_API_KEY unset, aborting")
			break batchLoop

		case notify.IsQuotaExceeded(err):
			failed += len(recipients) - i
			lastErrNote = fmt.Sprintf("quota 用盡，已寄 %d 封", sent)
			log.Warn().Err(err).Str("broadcast_id", id).Int("sent", sent).Msg("email broadcast: quota exceeded, stopping")
			break batchLoop

		default:
			failed += len(batch)
			lastErrNote = err.Error()
			log.Error().Err(err).Str("broadcast_id", id).Int("batch_start", i).Msg("email broadcast: batch send failed, continuing")
		}

		if end < len(recipients) {
			time.Sleep(broadcastBatchSleep)
		}
	}

	h.finishBroadcast(id, sent, failed, lastErrNote)
}

// broadcastStatus 純函式：依最終 sent/failed 數推導狀態，抽出來方便離線單元測試（比照
// race.isEntryRewardWindowActive 的「邏輯鏡射成純函式」慣例）。全部成功→done；有成功也有失敗
// →partial；一封都沒寄出（且確實嘗試過，failed>0）→failed。
func broadcastStatus(sent, failed int) string {
	if failed == 0 {
		return "done"
	}
	if sent == 0 {
		return "failed"
	}
	return "partial"
}

// finishBroadcast 寫回最終狀態（見 broadcastStatus）。
func (h *Handler) finishBroadcast(id string, sent, failed int, errNote string) {
	status := broadcastStatus(sent, failed)
	if status == "failed" {
		notify.Alert("mail_broadcast_fail", "站內信廣播整體失敗", fmt.Sprintf("broadcast_id=%s sent=%d failed=%d", id, sent, failed))
	}
	if _, err := h.db.Exec(context.Background(), `
		UPDATE email_broadcasts SET status=$1, sent_count=$2, fail_count=$3, error_note=$4, finished_at=NOW()
		WHERE id=$5`, status, sent, failed, errNote, id); err != nil {
		log.Error().Err(err).Str("broadcast_id", id).Msg("email broadcast: failed to write final status")
	}
}
