package organizer

import (
	"context"
	"errors"

	"github.com/dor/api/internal/race"
)

var (
	ErrNotVerified      = errors.New("organizer not verified by platform yet")
	ErrProfileNotFound  = errors.New("organizer profile not found")
	ErrNotOwner         = errors.New("race does not belong to this organizer")
)

type Service struct {
	repo     *Repository
	raceSvc  *race.Service
}

func NewService(repo *Repository, raceSvc *race.Service) *Service {
	return &Service{repo: repo, raceSvc: raceSvc}
}

// GetProfile 取得合作方 profile
func (s *Service) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	p, err := s.repo.GetProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrProfileNotFound
	}
	return p, nil
}

// UpsertProfile 建立或更新合作方 profile（首次填寫後才能申請賽事）
func (s *Service) UpsertProfile(ctx context.Context, userID string, p *Profile) error {
	p.UserID = userID
	return s.repo.UpsertProfile(ctx, p)
}

// GetDashboard 取得合作方總覽
func (s *Service) GetDashboard(ctx context.Context, userID string) (*Dashboard, error) {
	return s.repo.GetDashboard(ctx, userID)
}

// ListMyRaces 取得合作方自己的賽事
func (s *Service) ListMyRaces(ctx context.Context, userID string) ([]*RaceSummary, error) {
	return s.repo.ListRaces(ctx, userID)
}

// SubmitRace 合作方提交新賽事（進入 pending_review 狀態）
func (s *Service) SubmitRace(ctx context.Context, organizerID string, r *race.Race) (*race.Race, error) {
	// 必須先通過平台驗證
	verified, err := s.repo.IsVerified(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if !verified {
		return nil, ErrNotVerified
	}

	// 合作方提交的賽事：review_status=pending，status 先設 soon（審核通過後才生效）
	r.Status = "soon"
	// CreatedBy 由 handler 設入 organizerID

	return s.raceSvc.CreateRaceWithReview(ctx, r, "pending")
}

// GetMyRaceDetail 取得合作方自己某賽事的詳情（含審核狀態 + 報名）
func (s *Service) GetMyRaceDetail(ctx context.Context, organizerID, raceID string) (*race.Race, []*race.Registration, error) {
	// 確認此賽事屬於此合作方
	raceDetail, _, err := s.raceSvc.GetDetail(ctx, raceID, "")
	if err != nil {
		return nil, nil, err
	}
	if raceDetail == nil {
		return nil, nil, race.ErrRaceNotFound
	}
	if raceDetail.CreatedBy != organizerID {
		return nil, nil, ErrNotOwner
	}

	signups, err := s.raceSvc.AdminListSignups(ctx, raceID)
	if err != nil {
		return nil, nil, err
	}

	return raceDetail, signups, nil
}

// --- Admin operations ---

// ListPendingRaces admin 列出待審核賽事
func (s *Service) ListPendingRaces(ctx context.Context) ([]*RaceSummary, error) {
	return s.repo.ListPendingRaces(ctx)
}

// ReviewRace admin 審核賽事（approve | reject）
func (s *Service) ReviewRace(ctx context.Context, raceID, action, note, reviewerID string) error {
	if action != "approve" && action != "reject" {
		return errors.New("action must be 'approve' or 'reject'")
	}
	status := "approved"
	if action == "reject" {
		status = "rejected"
	}
	return s.repo.ReviewRace(ctx, raceID, status, note, reviewerID)
}

// ListOrganizers admin 列出所有合作方
func (s *Service) ListOrganizers(ctx context.Context) ([]map[string]any, error) {
	return s.repo.ListOrganizers(ctx)
}

// VerifyOrganizer admin 審核合作方資格
func (s *Service) VerifyOrganizer(ctx context.Context, targetUserID string, verified bool) error {
	return s.repo.SetVerified(ctx, targetUserID, verified)
}
