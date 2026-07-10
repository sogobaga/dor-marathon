// 帳號群組 CRUD：後台推播對象擴充用（target_type=group）。
// 契約細節見 migrations/061_account_groups.sql 與 Broadcast（push.go）。
package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// GroupAdminRouter 帳號群組 CRUD：掛在 /api/v1/admin/push-groups（外層已檢查 settings 權限）。
func (h *Handler) GroupAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", h.ListGroups)
	r.Post("/", h.CreateGroup)
	r.Post("/{id}/rename", h.RenameGroup)
	r.Post("/{id}/delete", h.DeleteGroup)
	r.Get("/{id}", h.GetGroup)
	r.Post("/{id}/members/add", h.AddGroupMembers)
	r.Post("/{id}/members/remove", h.RemoveGroupMember)
	return r
}

type groupSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

// GET /api/v1/admin/push-groups
func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		SELECT g.id::text, g.name, COUNT(m.user_id)
		FROM account_groups g
		LEFT JOIN account_group_members m ON m.group_id = g.id
		GROUP BY g.id, g.name
		ORDER BY g.created_at DESC
	`)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load groups")
		return
	}
	defer rows.Close()

	groups := []groupSummary{}
	for rows.Next() {
		var g groupSummary
		if err := rows.Scan(&g.ID, &g.Name, &g.MemberCount); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to scan groups")
			return
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load groups")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

// POST /api/v1/admin/push-groups
func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		respondErr(w, http.StatusBadRequest, "name required")
		return
	}

	var id string
	err := h.db.QueryRow(r.Context(), `
		INSERT INTO account_groups (name) VALUES ($1) RETURNING id::text
	`, name).Scan(&id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to create group")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"id": id})
}

// POST /api/v1/admin/push-groups/{id}/rename
func (h *Handler) RenameGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		respondErr(w, http.StatusBadRequest, "name required")
		return
	}

	if _, err := h.db.Exec(r.Context(), `UPDATE account_groups SET name = $1 WHERE id = $2`, name, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to rename group")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/v1/admin/push-groups/{id}/delete
func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if _, err := h.db.Exec(r.Context(), `DELETE FROM account_groups WHERE id = $1`, id); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to delete group")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type groupMember struct {
	UserID      string `json:"user_id"`
	AccountCode string `json:"account_code"`
	Name        string `json:"name"`
	Email       string `json:"email"`
}

// GET /api/v1/admin/push-groups/{id}
func (h *Handler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var name string
	if err := h.db.QueryRow(r.Context(), `SELECT name FROM account_groups WHERE id = $1`, id).Scan(&name); err != nil {
		respondErr(w, http.StatusNotFound, "group not found")
		return
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT u.id::text, COALESCE(u.account_code,''), u.name, u.email
		FROM account_group_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1
		ORDER BY u.name
	`, id)
	if err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load members")
		return
	}
	defer rows.Close()

	members := []groupMember{}
	for rows.Next() {
		var m groupMember
		if err := rows.Scan(&m.UserID, &m.AccountCode, &m.Name, &m.Email); err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to scan members")
			return
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to load members")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "members": members})
}

// POST /api/v1/admin/push-groups/{id}/members/add
// body: { identifiers: [string] } — 每個字串含 "@" 視為 email，否則去除開頭 "#" 後 UPPER 比對 account_code。
func (h *Handler) AddGroupMembers(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")

	var body struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	added := 0
	notFound := []string{}
	for _, raw := range body.Identifiers {
		ident := strings.TrimSpace(raw)
		if ident == "" {
			continue
		}

		userID, found, err := h.resolveIdentifier(r.Context(), ident)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to resolve identifier")
			return
		}
		if !found {
			notFound = append(notFound, raw)
			continue
		}

		ct, err := h.db.Exec(r.Context(), `
			INSERT INTO account_group_members (group_id, user_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, groupID, userID)
		if err != nil {
			respondErr(w, http.StatusInternalServerError, "failed to add member")
			return
		}
		if ct.RowsAffected() > 0 {
			added++
		}
	}

	respondJSON(w, http.StatusOK, map[string]any{"added": added, "not_found": notFound})
}

// POST /api/v1/admin/push-groups/{id}/members/remove
func (h *Handler) RemoveGroupMember(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")

	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondErr(w, http.StatusBadRequest, "invalid body")
		return
	}

	if _, err := h.db.Exec(r.Context(), `
		DELETE FROM account_group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, body.UserID); err != nil {
		respondErr(w, http.StatusInternalServerError, "failed to remove member")
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// resolveIdentifier 解析單一識別碼成 user_id：含 "@" 視為 email（不分大小寫比對）；
// 否則去掉開頭 "#"、轉大寫後比對 account_code。查無回 found=false（非 error）。
func (h *Handler) resolveIdentifier(ctx context.Context, identifier string) (userID string, found bool, err error) {
	ident := strings.TrimSpace(identifier)
	if ident == "" {
		return "", false, nil
	}

	var row pgx.Row
	if strings.Contains(ident, "@") {
		row = h.db.QueryRow(ctx, `SELECT id::text FROM users WHERE lower(email) = lower($1)`, ident)
	} else {
		code := strings.ToUpper(strings.TrimPrefix(ident, "#"))
		row = h.db.QueryRow(ctx, `SELECT id::text FROM users WHERE account_code = $1`, code)
	}

	if err := row.Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return userID, true, nil
}
