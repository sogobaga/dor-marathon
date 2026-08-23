package race

import (
	"context"
	"sort"
	"time"
)

// Certificate 完賽證明資料（前台用 canvas 繪製成圖下載）
type Certificate struct {
	Completed     bool       `json:"completed"`
	RaceTitle     string     `json:"race_title"`
	Name          string     `json:"name"` // 證書顯示姓名（真實姓名優先）
	GroupName     string     `json:"group_name,omitempty"`
	TargetKm      float64    `json:"target_km"`
	CompletedKm   float64    `json:"completed_km"`
	CompletionAt  *time.Time `json:"completion_at,omitempty"`
	TotalTimeS    int        `json:"total_time_s"`
	FinishRank    int        `json:"finish_rank"` // 完成時間名次
	FinishedCount int        `json:"finished_count"`
	RaceEnd       *time.Time `json:"race_end,omitempty"`
	RaceEnded     bool       `json:"race_ended"`       // 賽事是否已結束（迄日已過）
	BgURL         string     `json:"bg_url,omitempty"` // 後台自訂底圖（空=前台用預設設計）
}

// certInfo 取得證書顯示姓名與該使用者分組目標里程
func (r *Repository) certInfo(ctx context.Context, userID, raceID string) (name string, targetKm float64, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(p.real_name,''), u.name),
		       COALESCE(g.target_distance_km, 0)
		FROM users u
		LEFT JOIN user_profiles p ON p.user_id = u.id
		LEFT JOIN registrations reg ON reg.user_id = u.id AND reg.race_id = $2 AND reg.status <> 'cancelled'
		LEFT JOIN race_groups g ON g.id = reg.group_id
		WHERE u.id = $1`, userID, raceID).Scan(&name, &targetKm)
	return
}

// GetMyCertificate 取得登入者在某賽事的完賽證明資料
func (s *Service) GetMyCertificate(ctx context.Context, raceID, userID string) (*Certificate, error) {
	race, err := s.repo.GetByID(ctx, raceID)
	if err != nil {
		return nil, err
	}
	if race == nil || race.ReviewStatus != "approved" {
		return nil, ErrRaceNotFound
	}
	// personal 模式可重複挑戰，一般完賽證明（computeFinishers 依「分組目標里程」判定完成）不適用；
	// 前台改走完賽歷程（GetPersonalHistory），此端點防禦性擋下，避免對 personal 賽事誤算/誤顯示
	// （比照 GetPersonalProgress／GetPersonalLeaderboard 對「模式不符」一律回 ErrRaceNotFound 的慣例）。
	if race.EventMode == "personal" {
		return nil, ErrRaceNotFound
	}
	// 完賽證明顯示開關（config.certificate_disabled）：關閉時整塊不回傳，前台按鈕已隱藏，這裡是防繞過
	// （直接打 API 也拿不到資料）。
	if race.Config.CertificateDisabled {
		return nil, ErrCertificateDisabled
	}

	name, target, err := s.repo.certInfo(ctx, userID, raceID)
	if err != nil {
		return nil, err
	}
	end := race.EndDate
	cert := &Certificate{
		RaceTitle: race.Title,
		Name:      name,
		TargetKm:  target,
		RaceEnd:   &end,
		RaceEnded: time.Now().After(race.EndDate),
		BgURL:     race.CertificateBgURL,
	}

	finishers, _, err := s.repo.computeFinishers(ctx, raceID)
	if err != nil {
		return nil, err
	}
	cert.FinishedCount = len(finishers)
	// 依完成時間排序以取得名次
	sort.Slice(finishers, func(i, j int) bool { return finishers[i].completionAt.Before(finishers[j].completionAt) })
	for i, f := range finishers {
		if f.userID == userID {
			cert.Completed = true
			cert.GroupName = f.groupName
			cert.CompletedKm = round2(f.distanceKm)
			c := f.completionAt
			cert.CompletionAt = &c
			cert.TotalTimeS = f.totalTimeS
			cert.FinishRank = i + 1
			break
		}
	}
	return cert, nil
}
