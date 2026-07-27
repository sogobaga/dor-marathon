package monopoly

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dor/api/internal/wallet"
)

// ErrInsufficientGP GP 餘額不足，扣款直接失敗、絕不繼續擲骰（見 Roll）。
var ErrInsufficientGP = errors.New("insufficient gp")

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetState 讀取棋子狀態 + GP 餘額，供前台顯示（不異動任何資料；查無棋子狀態視為尚未擲過骰，position=0）。
func (r *Repository) GetState(ctx context.Context, userID string) (*PlayerState, error) {
	s := &PlayerState{}
	err := r.db.QueryRow(ctx,
		`SELECT position, laps_completed FROM monopoly_player_state WHERE user_id=$1`, userID,
	).Scan(&s.Position, &s.LapsCompleted)
	if errors.Is(err, pgx.ErrNoRows) {
		s.Position, s.LapsCompleted = 0, 0
	} else if err != nil {
		return nil, fmt.Errorf("get monopoly state: %w", err)
	}
	if err := r.db.QueryRow(ctx, `SELECT gp FROM users WHERE id=$1`, userID).Scan(&s.GPBalance); err != nil {
		return nil, fmt.Errorf("get gp balance: %w", err)
	}
	return s, nil
}

// Roll 擲骰的完整原子流程，單一交易內完成，任一步失敗整段復原（扣了 GP 就一定會移動/寫紀錄，
// 絕不會出現「扣款成功但棋子沒動」的半途而廢）：
//  1. 用 wallet.SpendGP CAS 扣款；餘額不足直接回 ErrInsufficientGP，交易復原、完全不繼續。
//  2. 伺服器決定點數（crypto/rand，前端無法影響——防作弊關鍵）。
//  3. 鎖列讀出目前位置（SELECT ... FOR UPDATE，序列化同一使用者的併發擲骰請求，不會互相覆蓋）。
//  4. 計算新位置/圈數並寫回；繞圈才發 lap 獎勵；寫入 monopoly_rolls 流水。
func (r *Repository) Roll(ctx context.Context, userID string, diceCost, lapRewardGP int) (*RollResult, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	ok, err := wallet.SpendGP(ctx, tx, userID, diceCost, "monopoly_roll", "monopoly", nil)
	if err != nil {
		return nil, fmt.Errorf("spend gp: %w", err)
	}
	if !ok {
		return nil, ErrInsufficientGP
	}

	roll, err := secureRoll()
	if err != nil {
		return nil, err
	}

	// 確保有列可鎖（首次擲骰自動建立初始狀態：position=0, laps_completed=0）。
	if _, err := tx.Exec(ctx,
		`INSERT INTO monopoly_player_state (user_id) VALUES ($1) ON CONFLICT (user_id) DO NOTHING`, userID,
	); err != nil {
		return nil, fmt.Errorf("ensure player state: %w", err)
	}
	var from, prevLaps int
	if err := tx.QueryRow(ctx,
		`SELECT position, laps_completed FROM monopoly_player_state WHERE user_id=$1 FOR UPDATE`, userID,
	).Scan(&from, &prevLaps); err != nil {
		return nil, fmt.Errorf("lock player state: %w", err)
	}

	raw := from + roll
	lapsGained := raw / boardSize
	to := raw % boardSize

	if _, err := tx.Exec(ctx,
		`UPDATE monopoly_player_state SET position=$2, laps_completed=laps_completed+$3, updated_at=NOW() WHERE user_id=$1`,
		userID, to, lapsGained,
	); err != nil {
		return nil, fmt.Errorf("update player state: %w", err)
	}

	landedOn := landedOnFor(to)

	lapRewardTotal := 0
	if lapsGained > 0 && lapRewardGP > 0 {
		lapRewardTotal = lapRewardGP * lapsGained
		if err := wallet.AwardGP(ctx, tx, userID, lapRewardTotal, "monopoly_lap", "monopoly", nil); err != nil {
			return nil, fmt.Errorf("award lap gp: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO monopoly_rolls (user_id, roll_value, from_position, to_position, gp_cost, laps_gained, landed_on)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		userID, roll, from, to, diceCost, lapsGained, landedOn,
	); err != nil {
		return nil, fmt.Errorf("insert roll record: %w", err)
	}

	var gpBalance int
	if err := tx.QueryRow(ctx, `SELECT gp FROM users WHERE id=$1`, userID).Scan(&gpBalance); err != nil {
		return nil, fmt.Errorf("read gp balance: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	return &RollResult{
		Roll: roll, From: from, To: to, LapsGained: lapsGained, LandedOn: landedOn,
		LapRewardGP: lapRewardTotal, GPBalance: gpBalance,
		DrawPending: landedOn == "chance" || landedOn == "destiny",
	}, nil
}

// secureRoll 伺服器端決定 1..6 點數，用 crypto/rand（而非 math/rand）——這是防作弊的關鍵，
// 前端完全無法預測或影響結果，只能等後端回應後把動畫收尾在這個值上。
func secureRoll() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(6))
	if err != nil {
		return 0, fmt.Errorf("roll rand: %w", err)
	}
	return int(n.Int64()) + 1, nil
}
