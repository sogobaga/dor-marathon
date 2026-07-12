package stamina

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SPState 供 dashboard 顯示體力條。
type SPState struct {
	SP             int        `json:"sp"`
	SPMax          int        `json:"sp_max"`
	RecoverMin     int        `json:"sp_recover_min"`     // 每恢復 1 點所需分鐘（依水準）
	NextRecoverSec int        `json:"sp_next_recover_sec"` // 距下一點恢復秒數（0=已滿）
	FreezeUntil    *time.Time `json:"sp_freeze_until"`     // 過度訓練凍結到此時間（nil=無）
	Fitness        int        `json:"fitness"`
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// levelForExp 依 exp 查 level_config 算等級（同 profile.computeLevel 邏輯）。
func levelForExp(ctx context.Context, db *pgxpool.Pool, exp int) int {
	rows, err := db.Query(ctx, `SELECT level, exp_required FROM level_config ORDER BY exp_required DESC`)
	if err != nil {
		return 1
	}
	defer rows.Close()
	for rows.Next() {
		var lv, req int
		if rows.Scan(&lv, &req) == nil && exp >= req {
			return lv
		}
	}
	return 1
}

// computeFitness 依近 6 週未 flagged 活動算跑步水準(0-100)與閾值配速 T(秒/km)。
func computeFitness(ctx context.Context, db *pgxpool.Pool, uid string) (fitness, thresholdPaceS int) {
	var cnt int
	var sumKm float64
	var best *int
	err := db.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(distance_km),0),
		       MIN(avg_pace_s) FILTER (WHERE distance_km >= 3 AND avg_pace_s > 0)
		FROM activities
		WHERE user_id=$1 AND NOT flagged AND recorded_at > NOW() - INTERVAL '42 days'`, uid).
		Scan(&cnt, &sumKm, &best)
	if err != nil {
		return 0, 420
	}
	T := 420 // 無資料預設 7:00/km
	if best != nil && *best > 0 {
		T = *best
	}
	T = clampInt(T, 180, 600)
	paceScore := clampInt((480-T)*100/240, 0, 100) // 4:00→100、8:00→0
	volScore := clampInt(int(sumKm/6.0/80.0*100), 0, 100)
	freqScore := clampInt(int(float64(cnt)/6.0/7.0*100), 0, 100)
	fitness = (paceScore*5 + volScore*3 + freqScore*2) / 10
	return clampInt(fitness, 0, 100), T
}

// ChargeSP 一趟跑步完成後扣 SP（best-effort：先懶惰恢復→扣→扣到 0 凍結 6 小時）。錯誤僅記 log。
func ChargeSP(ctx context.Context, db *pgxpool.Pool, uid string, distanceKm float64, avgPaceS int) {
	if db == nil || uid == "" || distanceKm <= 0 {
		return
	}
	var sp, exp int
	var updatedAt time.Time
	var freeze *time.Time
	if err := db.QueryRow(ctx, `SELECT sp, sp_updated_at, sp_freeze_until, exp FROM users WHERE id=$1`, uid).
		Scan(&sp, &updatedAt, &freeze, &exp); err != nil {
		log.Printf("stamina.ChargeSP: load user %s: %v", uid, err)
		return
	}
	level := levelForExp(ctx, db, exp)
	spMax := SPMax(level)
	fitness, T := computeFitness(ctx, db, uid)
	now := time.Now()
	sp, _ = Accrue(sp, spMax, updatedAt, freeze, now, RecoveryMin(fitness))
	cost := Cost(distanceKm, IntensityRate(avgPaceS, T))
	newSP := sp - cost
	var newFreeze *time.Time
	if newSP < 0 {
		newSP = 0
		f := now.Add(FreezeDuration)
		newFreeze = &f
	}
	if _, err := db.Exec(ctx,
		`UPDATE users SET sp=$2, sp_updated_at=$3, sp_freeze_until=$4, fitness_score=$5, fitness_updated_at=$3 WHERE id=$1`,
		uid, newSP, now, newFreeze, fitness); err != nil {
		log.Printf("stamina.ChargeSP: update user %s: %v", uid, err)
	}
}

// ReadSP 讀取(懶惰恢復後)目前體力狀態，供 dashboard。level 由呼叫端(已算好)傳入。
func ReadSP(ctx context.Context, db *pgxpool.Pool, uid string, level int) (SPState, error) {
	var sp, fitness int
	var updatedAt time.Time
	var freeze *time.Time
	if err := db.QueryRow(ctx, `SELECT sp, sp_updated_at, sp_freeze_until, fitness_score FROM users WHERE id=$1`, uid).
		Scan(&sp, &updatedAt, &freeze, &fitness); err != nil {
		return SPState{}, err
	}
	spMax := SPMax(level)
	recMin := RecoveryMin(fitness)
	now := time.Now()
	newSP, newUpdated := Accrue(sp, spMax, updatedAt, freeze, now, recMin)
	effFreeze := freeze
	if effFreeze != nil && !effFreeze.After(now) {
		effFreeze = nil // 凍結已過 → 視為無
	}
	if newSP != sp || effFreeze != freeze {
		if _, err := db.Exec(ctx, `UPDATE users SET sp=$2, sp_updated_at=$3, sp_freeze_until=$4 WHERE id=$1`,
			uid, newSP, newUpdated, effFreeze); err != nil {
			log.Printf("stamina.ReadSP: persist %s: %v", uid, err)
		}
	}
	return SPState{
		SP: newSP, SPMax: spMax, RecoverMin: recMin, Fitness: fitness,
		NextRecoverSec: NextRecoverSeconds(newSP, spMax, newUpdated, effFreeze, now, recMin),
		FreezeUntil:    effFreeze,
	}, nil
}
