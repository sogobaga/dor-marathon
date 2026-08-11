package activityreward

// repository.go 全域即時獎勵模板（reward_templates）CRUD 資料存取。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// itemsToBytes 將 []RewardItem 序列化為 JSON bytes 供寫入 JSONB；nil 一律存成 '[]'（非 NULL），
// 讀取端因此不必處理 NULL 分支（比照 reward_templates.items 欄位 NOT NULL DEFAULT '[]' 的設計）。
func itemsToBytes(items []RewardItem) ([]byte, error) {
	if items == nil {
		items = []RewardItem{}
	}
	return json.Marshal(items)
}

func bytesToItems(b []byte) ([]RewardItem, error) {
	items := []RewardItem{}
	if len(b) == 0 {
		return items, nil
	}
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (r *Repository) ListTemplates(ctx context.Context) ([]Template, error) {
	rows, err := r.db.Query(ctx, `SELECT id, name, items, created_at FROM reward_templates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		var t Template
		var itemsBytes []byte
		if err := rows.Scan(&t.ID, &t.Name, &itemsBytes, &t.CreatedAt); err != nil {
			return nil, err
		}
		if t.Items, err = bytesToItems(itemsBytes); err != nil {
			return nil, fmt.Errorf("parse template items: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repository) CreateTemplate(ctx context.Context, name string, items []RewardItem) (*Template, error) {
	itemsBytes, err := itemsToBytes(items)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}
	t := &Template{Name: name, Items: items}
	err = r.db.QueryRow(ctx,
		`INSERT INTO reward_templates (name, items) VALUES ($1,$2) RETURNING id, created_at`,
		name, itemsBytes).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}
	return t, nil
}

func (r *Repository) UpdateTemplate(ctx context.Context, id, name string, items []RewardItem) (*Template, error) {
	itemsBytes, err := itemsToBytes(items)
	if err != nil {
		return nil, fmt.Errorf("marshal items: %w", err)
	}
	t := &Template{ID: id, Name: name, Items: items}
	err = r.db.QueryRow(ctx,
		`UPDATE reward_templates SET name=$1, items=$2 WHERE id=$3 RETURNING created_at`,
		name, itemsBytes, id).Scan(&t.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *Repository) DeleteTemplate(ctx context.Context, id string) error {
	ct, err := r.db.Exec(ctx, `DELETE FROM reward_templates WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
