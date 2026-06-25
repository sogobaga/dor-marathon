package organizer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// GetProfile 取得合作方 profile
func (r *Repository) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	p := &Profile{}
	err := r.db.QueryRow(ctx, `
		SELECT user_id, company_name,
		       COALESCE(contact_name,'')  as contact_name,
		       COALESCE(contact_email,'') as contact_email,
		       COALESCE(contact_phone,'') as contact_phone,
		       COALESCE(website,'')       as website,
		       COALESCE(description,'')   as description,
		       verified, verified_at, created_at
		FROM organizer_profiles WHERE user_id = $1
	`, userID).Scan(
		&p.UserID, &p.CompanyName, &p.ContactName, &p.ContactEmail,
		&p.ContactPhone, &p.Website, &p.Description,
		&p.Verified, &p.VerifiedAt, &p.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// UpsertProfile 建立或更新合作方 profile
func (r *Repository) UpsertProfile(ctx context.Context, p *Profile) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO organizer_profiles
		    (user_id, company_name, contact_name, contact_email, contact_phone, website, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id) DO UPDATE SET
		    company_name  = EXCLUDED.company_name,
		    contact_name  = EXCLUDED.contact_name,
		    contact_email = EXCLUDED.contact_email,
		    contact_phone = EXCLUDED.contact_phone,
		    website       = EXCLUDED.website,
		    description   = EXCLUDED.description,
		    updated_at    = NOW()
	`, p.UserID, p.CompanyName, p.ContactName, p.ContactEmail,
		p.ContactPhone, p.Website, p.Description)
	return err
}

// IsVerified 確認合作方是否已通過平台審核
func (r *Repository) IsVerified(ctx context.Context, userID string) (bool, error) {
	var verified bool
	err := r.db.QueryRow(ctx, `SELECT verified FROM organizer_profiles WHERE user_id=$1`, userID).Scan(&verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return verified, err
}

// SetVerified admin 審核合作方
func (r *Repository) SetVerified(ctx context.Context, userID string, verified bool) error {
	var verifiedAt interface{}
	if verified {
		verifiedAt = "NOW()"
	}
	_ = verifiedAt
	_, err := r.db.Exec(ctx, `
		UPDATE organizer_profiles
		SET verified = $1, verified_at = CASE WHEN $1 THEN NOW() ELSE NULL END, updated_at = NOW()
		WHERE user_id = $2
	`, verified, userID)
	return err
}

// ListRaces 取得合作方自己的賽事（含審核狀態 + 報名人數）
func (r *Repository) ListRaces(ctx context.Context, organizerID string) ([]*RaceSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.slug, r.title, r.status, r.review_status,
		       COALESCE(r.review_note,'') as review_note,
		       r.start_date, r.end_date, r.created_at,
		       COUNT(reg.id) as signup_count
		FROM races r
		LEFT JOIN registrations reg ON reg.race_id = r.id AND reg.status = 'paid'
		WHERE r.created_by = $1
		GROUP BY r.id
		ORDER BY r.created_at DESC
	`, organizerID)
	if err != nil {
		return nil, fmt.Errorf("list organizer races: %w", err)
	}
	defer rows.Close()

	var races []*RaceSummary
	for rows.Next() {
		s := &RaceSummary{}
		if err := rows.Scan(&s.ID, &s.Slug, &s.Title, &s.Status, &s.ReviewStatus,
			&s.ReviewNote, &s.StartDate, &s.EndDate, &s.CreatedAt, &s.SignupCount); err != nil {
			return nil, err
		}
		races = append(races, s)
	}
	return races, rows.Err()
}

// GetDashboard 取得合作方總覽數據
func (r *Repository) GetDashboard(ctx context.Context, organizerID string) (*Dashboard, error) {
	d := &Dashboard{}
	err := r.db.QueryRow(ctx, `
		SELECT
		    COUNT(r.id)                                                   as total_races,
		    COUNT(r.id) FILTER (WHERE r.review_status = 'pending')        as pending_races,
		    COUNT(r.id) FILTER (WHERE r.status IN ('open','live'))         as active_races,
		    COALESCE(SUM(reg_counts.cnt), 0)                              as total_signups,
		    COALESCE(SUM(rev_counts.revenue), 0)                          as total_revenue
		FROM races r
		LEFT JOIN (
		    SELECT race_id, COUNT(*) as cnt
		    FROM registrations WHERE status='paid' GROUP BY race_id
		) reg_counts ON reg_counts.race_id = r.id
		LEFT JOIN (
		    SELECT race_id, SUM(amount) as revenue
		    FROM registrations WHERE status='paid' GROUP BY race_id
		) rev_counts ON rev_counts.race_id = r.id
		WHERE r.created_by = $1
	`, organizerID).Scan(
		&d.TotalRaces, &d.PendingRaces, &d.ActiveRaces, &d.TotalSignups, &d.TotalRevenue,
	)
	return d, err
}

// --- Admin-only ---

// ListPendingRaces 列出所有待審核的合作方賽事
func (r *Repository) ListPendingRaces(ctx context.Context) ([]*RaceSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT r.id, r.slug, r.title, r.status, r.review_status,
		       COALESCE(r.review_note,'') as review_note,
		       r.start_date, r.end_date, r.created_at,
		       COUNT(reg.id) as signup_count
		FROM races r
		LEFT JOIN registrations reg ON reg.race_id = r.id AND reg.status = 'paid'
		WHERE r.review_status = 'pending'
		GROUP BY r.id
		ORDER BY r.created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list pending races: %w", err)
	}
	defer rows.Close()

	var races []*RaceSummary
	for rows.Next() {
		s := &RaceSummary{}
		if err := rows.Scan(&s.ID, &s.Slug, &s.Title, &s.Status, &s.ReviewStatus,
			&s.ReviewNote, &s.StartDate, &s.EndDate, &s.CreatedAt, &s.SignupCount); err != nil {
			return nil, err
		}
		races = append(races, s)
	}
	return races, rows.Err()
}

// ReviewRace 更新賽事審核狀態（admin 操作）
func (r *Repository) ReviewRace(ctx context.Context, raceID, status, note, reviewerID string) error {
	// 核准時同步將賽事改為 'soon'（可上線）；退回時維持 'soon' 但打上 rejected
	newRaceStatus := ""
	if status == "approved" {
		newRaceStatus = "soon"
	}

	if newRaceStatus != "" {
		_, err := r.db.Exec(ctx, `
			UPDATE races
			SET review_status=$1, review_note=$2, reviewed_by=$3, reviewed_at=NOW(),
			    status=$4, updated_at=NOW()
			WHERE id=$5
		`, status, note, reviewerID, newRaceStatus, raceID)
		return err
	}

	_, err := r.db.Exec(ctx, `
		UPDATE races
		SET review_status=$1, review_note=$2, reviewed_by=$3, reviewed_at=NOW(), updated_at=NOW()
		WHERE id=$4
	`, status, note, reviewerID, raceID)
	return err
}

// ListOrganizers admin 列出所有合作方申請
func (r *Repository) ListOrganizers(ctx context.Context) ([]map[string]any, error) {
	rows, err := r.db.Query(ctx, `
		SELECT u.id, u.email, u.name, u.handle, u.created_at,
		       p.company_name, p.verified, p.verified_at
		FROM users u
		JOIN organizer_profiles p ON p.user_id = u.id
		WHERE u.role = 'organizer'
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var id, email, name, handle, company string
		var verified bool
		var createdAt, verifiedAt interface{}
		if err := rows.Scan(&id, &email, &name, &handle, &createdAt,
			&company, &verified, &verifiedAt); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{
			"id": id, "email": email, "name": name, "handle": handle,
			"company_name": company, "verified": verified,
			"verified_at": verifiedAt, "created_at": createdAt,
		})
	}
	return result, rows.Err()
}
