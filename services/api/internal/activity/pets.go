package activity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// 寵物雲端馬拉松：活動 × 寵物歸戶（2026-09-09 owner request，migration 174，D3(b)）。
//
// GET/POST /api/v1/activities/{activityID}/pets 讓飼主替「自己的任一趟活動」（不限來源——GPS、
// Strava、Terra、後台補登皆可，見 D3(b)）事後補登/調整「這趟狗狗有一起跑」的歸戶名單，跟 GPS
// 上傳當下（gps.go SaveGPSRun）的即時歸戶走同一張 pet_activities 表、同一個 source='owner_run'，
// 差別只在「什麼時候寫入」——這裡刻意不限制活動所屬賽事的時間窗（D3(b)：「歸戶允許隨時，計分才
// 套賽事時間窗」），時間窗過濾留給 race 套件的計分查詢做。

var (
	// ErrActivityNotFound 呼叫者名下查無此活動（不存在，或存在但不是這個 user 的——兩者刻意回同一
	// 錯誤，不洩漏「這個 activity id 是否存在」給非本人）。
	ErrActivityNotFound = errors.New("活動不存在")
	// ErrPetOwnershipMismatch petIDs 有一筆（或以上）不屬於呼叫者名下的寵物報名紀錄。
	ErrPetOwnershipMismatch = errors.New("寵物名單有誤")
)

// ownedPetMeta 是某使用者名下、且屬於 petIDs 名單內的寵物在 registration_pets 的歸戶座標
// （registration_id/race_id）——LoadOwnedPetMeta 一次查完，回傳筆數若少於 petIDs 去重後的筆數，
// 代表有 ID 不存在或不屬於這個使用者（見呼叫端 SetActivityPets 的比對）。
type ownedPetMeta struct {
	PetID          string
	RegistrationID string
	RaceID         string
}

// LoadOwnedPetMeta 查詢 petIDs 名下屬於 userID、且所屬報名「目前未取消」的寵物報名座標（見
// ownedPetMeta 註解）。⚠️ 2026-09-09 review 修正：加上 reg.status <> 'cancelled'——理由與
// activity/repository.go ValidateOwnedPets 同款修正完全相同（擋已取消/退費報名的寵物被拿來繼續
// 歸戶新的活動；即使不擋，計分端 pet_scoring.go petActivityRegJoinSQL 也會在讀取時把這類列濾掉，
// 這裡補上是避免使用者以為「歸戶成功」卻永遠不計分的困惑體驗，不是計分正確性的最後一道防線）。
func (r *Repository) LoadOwnedPetMeta(ctx context.Context, userID string, petIDs []string) ([]ownedPetMeta, error) {
	if len(petIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT rp.id::text, rp.registration_id::text, rp.race_id::text
		FROM registration_pets rp
		JOIN registrations reg ON reg.id = rp.registration_id AND reg.status <> 'cancelled'
		WHERE rp.id = ANY($1::uuid[]) AND rp.user_id = $2`, petIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("load owned pet meta: %w", err)
	}
	defer rows.Close()
	out := make([]ownedPetMeta, 0, len(petIDs))
	for rows.Next() {
		var m ownedPetMeta
		if err := rows.Scan(&m.PetID, &m.RegistrationID, &m.RaceID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetActivityPetIDs 查詢某活動目前的寵物歸戶名單（僅回傳呼叫者名下的——activity_id+user_id
// 一起篩，非本人的活動查無資料，跟「活動不存在」無法區分，刻意如此不洩漏他人資料）。
func (r *Repository) GetActivityPetIDs(ctx context.Context, activityID, userID string) ([]string, error) {
	rows, err := r.db.Query(ctx, `
		SELECT registration_pet_id::text FROM pet_activities
		WHERE activity_id = $1 AND user_id = $2 AND source = 'owner_run'
		ORDER BY created_at`, activityID, userID)
	if err != nil {
		return nil, fmt.Errorf("get activity pet ids: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ReplaceActivityPets 交易內整批重建某活動的 owner_run 寵物歸戶：先確認活動屬於 userID（否則
// ErrActivityNotFound），刪除不在新名單內的舊列，插入新增的（ON CONFLICT DO NOTHING 靠
// migrations/174 的 partial unique index 冪等，已存在的維持原樣不覆寫）。
//
// meta 為呼叫端先用 LoadOwnedPetMeta 驗證過、確定屬於 userID 的寵物名單（含 registration_id/race_id）
// ——這裡不重複驗證擁有權，只信任呼叫端已經做過（見 Service.SetActivityPets）。
func (r *Repository) ReplaceActivityPets(ctx context.Context, userID, activityID string, meta []ownedPetMeta) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // Commit 後為 no-op

	var distanceKm float64
	var durationS int
	var recordedAt interface{}
	err = tx.QueryRow(ctx,
		`SELECT distance_km, duration_s, recorded_at FROM activities WHERE id=$1 AND user_id=$2`,
		activityID, userID).Scan(&distanceKm, &durationS, &recordedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrActivityNotFound
	}
	if err != nil {
		return fmt.Errorf("load activity: %w", err)
	}

	keepIDs := make([]string, len(meta))
	for i, m := range meta {
		keepIDs[i] = m.PetID
	}
	// 刪除不在新名單內的舊列（NOT (x = ANY('{}')) 對空陣列恆為 TRUE，keepIDs 為空時等同清空全部歸戶，
	// 對應「沒有帶狗」重新取消勾選的情境）。
	if _, err := tx.Exec(ctx,
		`DELETE FROM pet_activities WHERE activity_id=$1 AND source='owner_run' AND NOT (registration_pet_id = ANY($2::uuid[]))`,
		activityID, keepIDs); err != nil {
		return fmt.Errorf("delete removed pet_activities: %w", err)
	}
	for _, m := range meta {
		if _, err := tx.Exec(ctx, `
			INSERT INTO pet_activities (registration_pet_id, registration_id, race_id, user_id, activity_id,
			                            source, distance_km, duration_s, recorded_at)
			VALUES ($1,$2,$3,$4,$5,'owner_run',$6,$7,$8)
			ON CONFLICT (registration_pet_id, activity_id) DO NOTHING`,
			m.PetID, m.RegistrationID, m.RaceID, userID, activityID, distanceKm, durationS, recordedAt); err != nil {
			return fmt.Errorf("insert pet_activities: %w", err)
		}
	}
	return tx.Commit(ctx)
}
