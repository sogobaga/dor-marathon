package runmeet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Repository struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) *Repository { return &Repository{db: db} }

// meetCols 團練列的共用 SELECT 欄位。
// ⚠️ join_password_hash **永遠不在這裡**——只推導出布林 is_private。整個套件 grep
// join_password_hash 應只命中 INSERT / UPDATE / 密碼驗證查詢三處（規格 1.4）。
const meetCols = `
	m.id, m.owner_id, m.title, m.meet_at, m.region, m.place_label, m.lat, m.lng, m.meeting_detail,
	m.no_location,
	m.capacity, m.description, m.image_urls, m.image_limit, m.approval_required,
	(m.join_password_hash IS NOT NULL) AS is_private,
	m.member_count, m.pending_count, m.status, m.hidden_by_admin, m.hidden_by_owner, m.hidden_reason,
	m.comment_count, m.reaction_count, m.created_at, m.updated_at,
	COALESCE(NULLIF(u.name,''), u.handle) AS owner_name,
	COALESCE(u.avatar_url,'') AS owner_avatar,
	mm.status AS my_status, rr.kind AS my_reaction,
	(ac.user_id IS NOT NULL) AS unlocked`

// meetJoins 觀看者視角三個 LEFT JOIN（$1 一律是觀看者 uid）。
const meetJoins = `
	FROM run_meets m
	JOIN users u ON u.id = m.owner_id
	LEFT JOIN run_meet_members   mm ON mm.meet_id = m.id AND mm.user_id = $1
	LEFT JOIN run_meet_reactions rr ON rr.meet_id = m.id AND rr.user_id = $1
	LEFT JOIN run_meet_access    ac ON ac.meet_id = m.id AND ac.user_id = $1`

func scanMeet(row interface{ Scan(...any) error }) (meetRow, error) {
	var m meetRow
	err := row.Scan(&m.ID, &m.OwnerID, &m.Title, &m.MeetAt, &m.Region, &m.PlaceLabel, &m.Lat, &m.Lng, &m.MeetingDetail,
		&m.NoLocation,
		&m.Capacity, &m.Description, &m.ImageURLs, &m.ImageLimit, &m.ApprovalRequired, &m.IsPrivate,
		&m.MemberCount, &m.PendingCount, &m.Status, &m.HiddenByAdmin, &m.HiddenByOwner, &m.HiddenReason,
		&m.CommentCount, &m.ReactionCount, &m.CreatedAt, &m.UpdatedAt,
		&m.OwnerName, &m.OwnerAvatar, &m.MyStatus, &m.MyReaction, &m.Unlocked)
	return m, err
}

// --- 使用者旗標 ---

// UserFlags 一次查齊入口閘門與配額需要的使用者屬性。
func (r *Repository) UserFlags(ctx context.Context, uid string) (email, code string, isSuperAdmin, isVIP bool, err error) {
	err = r.db.QueryRow(ctx, `
		SELECT COALESCE(email,''), COALESCE(account_code,''), is_super_admin,
		       COALESCE(vip_expires_at > NOW(), FALSE)
		  FROM users WHERE id=$1`, uid).Scan(&email, &code, &isSuperAdmin, &isVIP)
	return
}

// QuotaOf 讀某人目前的配額計數（不扣點）。回傳的 month 可能是舊月份，呼叫端自行與台北當月比對。
func (r *Repository) QuotaOf(ctx context.Context, uid string) (month string, used int, err error) {
	var mm *string
	err = r.db.QueryRow(ctx, `SELECT run_meet_month, COALESCE(run_meet_used,0) FROM users WHERE id=$1`, uid).
		Scan(&mm, &used)
	if mm != nil {
		month = *mm
	}
	return
}

// --- 圖片引用歸屬驗證 ---

// VerifyImageOwnership 確認這批 image URL 對應的圖都是「本人上傳、purpose='runmeet'」。
// 防的是盜連他人上傳的圖或後台圖（那些圖的 owner_user_id 為 NULL 或別人）。
func (r *Repository) VerifyImageOwnership(ctx context.Context, uid string, urls []string) error {
	if len(urls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(urls))
	for _, u := range urls {
		if !ValidImageURL(u) {
			return errImageSource
		}
		ids = append(ids, imageIDFromURL(u))
	}
	var n int
	if err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM images
		 WHERE id = ANY($1::uuid[]) AND owner_user_id = $2 AND purpose = 'runmeet'`,
		ids, uid).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return errImageSource
	}
	return nil
}

// --- 建立 ---

// CreateMeet 配額 CAS ＋ INSERT ＋ 建立 owner 成員列，同一交易。
//
// 冪等：client_token 命中部分唯一索引（23505）時回傳既有那筆 id 並 dup=true，
// 呼叫端回 200 + 既有資料，**不重複扣點**（配額 CAS 與 INSERT 同交易，rollback 一起退）。
func (r *Repository) CreateMeet(ctx context.Context, uid string, in *MeetInput, imageLimit, quotaCap int, month string) (id string, used int, dup bool, err error) {
	// 先查冪等：同一個 owner + client_token 已存在就直接回（避免走進交易再靠 23505 rollback，
	// 那條路徑仍保留當作真正併發下的最後防線）。
	if in.ClientToken != "" {
		var existing string
		e := r.db.QueryRow(ctx, `SELECT id FROM run_meets WHERE owner_id=$1 AND client_token=$2`,
			uid, in.ClientToken).Scan(&existing)
		if e == nil {
			_, u, _ := r.QuotaOf(ctx, uid)
			return existing, u, true, nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return "", 0, false, e
		}
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return "", 0, false, err
	}
	defer tx.Rollback(ctx)

	// 1) 扣配額（必須在 INSERT 之前；失敗整包 rollback，不會白扣）
	used, err = consumeQuota(ctx, tx, uid, month, quotaCap)
	if errors.Is(err, ErrQuotaExhausted) && in.ClientToken != "" {
		// ⚠️ 冪等的第三道（別刪）：真正併發的兩個同 client_token 請求裡，第二個會先卡在
		// consumeQuota 的 users 列鎖上；第一個 commit 後 PostgreSQL 以 EvalPlanQual 重評
		// WHERE，額度已滿 → 回 0 列 → 直接 return，**永遠走不到下面 INSERT 的 23505 冪等分支**。
		// 非 VIP cap=1 代表「每一次建立都落在這個邊界」，不補這段的話使用者會看到
		// 409「本月發起次數已用完」，但團其實已經被第一個請求建好了（規格驗收條件 5 會掛）。
		var existing string
		if e := r.db.QueryRow(ctx, `SELECT id FROM run_meets WHERE owner_id=$1 AND client_token=$2`,
			uid, in.ClientToken).Scan(&existing); e == nil {
			_, u, _ := r.QuotaOf(ctx, uid)
			return existing, u, true, nil
		}
	}
	if err != nil {
		return "", 0, false, err
	}

	// 2) 密碼（私密團）：bcrypt，明碼與 hash 都不回讀
	var hash *string
	if in.Password != nil && *in.Password != "" {
		h, e := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
		if e != nil {
			return "", 0, false, e
		}
		s := string(h)
		hash = &s
	}

	var token *string
	if in.ClientToken != "" {
		t := in.ClientToken
		token = &t
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO run_meets
			(owner_id, title, meet_at, region, place_label, lat, lng, meeting_detail,
			 capacity, description, image_urls, image_limit, approval_required, no_location, join_password_hash,
			 member_count, pending_count, status, quota_month, client_token)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,1,0,'open',$16,$17)
		RETURNING id`,
		uid, in.Title, in.MeetAt, in.Region, in.PlaceLabel, in.Lat, in.Lng, in.MeetingDetail,
		in.Capacity, in.Description, in.ImageURLs, imageLimit, in.ApprovalRequired, in.NoLocation, hash,
		month, token).Scan(&id)
	if err != nil {
		// 併發連點：兩個請求同時通過上面的預查 → 唯一索引擋下第二個。
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "client_token") {
			// rollback（defer）會把這次的扣點一併退掉，然後回既有那筆
			var existing string
			if e := r.db.QueryRow(ctx, `SELECT id FROM run_meets WHERE owner_id=$1 AND client_token=$2`,
				uid, in.ClientToken).Scan(&existing); e == nil {
				_, u, _ := r.QuotaOf(ctx, uid)
				return existing, u, true, nil
			}
		}
		return "", 0, false, err
	}

	// 3) 發起人自己的成員列（role='owner'、status='joined'，計入 member_count=1）
	if _, err = tx.Exec(ctx, `
		INSERT INTO run_meet_members (meet_id, user_id, role, status, joined_at)
		VALUES ($1,$2,'owner','joined',NOW())`, id, uid); err != nil {
		return "", 0, false, err
	}

	if err = tx.Commit(ctx); err != nil {
		return "", 0, false, err
	}
	return id, used, false, nil
}

// --- 讀取 ---

// ListFilter 探索列表查詢條件。
// ⚠️ NearLat/NearLng 只當查詢參數用：不寫入資料庫、不寫進 log（使用者位置是機敏資料）。
type ListFilter struct {
	Q        string
	Region   string
	Privacy  string // ""|public|private
	Approval string // ""|free|review
	HasSlot  bool
	Ended    bool // true＝已結束折疊區
	Sort     string
	Limit    int
	Offset   int

	NearLat  *float64
	NearLng  *float64
	RadiusKm float64
}

// nearCandidateCap 附近搜尋時 bounding box 粗篩的最大候選筆數。
// 精算距離與排序在 Go 端做，必須有上限避免大半徑查詢把整張表撈進記憶體。
const nearCandidateCap = 500

// ListMeets 探索列表。回傳的 meetRow 仍含成員層欄位（lat/lng/meeting_detail），
// **由 handler 依觀看者身分決定要放進哪個 DTO**——列表一律走 CardView（沒有那些欄位）。
//
// ⚠️ 附近搜尋刻意**不**把私密團或「我不是成員的團」排除掉：需求 2(c) 明訂私密團也要出現在
// 探索列表，把它們從「附近」這個子檢視偷偷拿掉只是讓功能對正常使用者變爛。真正的防線是
// 「距離用量化座標算 + 只回 band + 半徑離散化」三件事（見 geo.go 的長註解與下面 near 分支），
// 讓非成員從附近搜尋能得到的資訊，不比公開層的 region/place_label 多。改這段前先讀完 geo.go。
func (r *Repository) ListMeets(ctx context.Context, uid string, f ListFilter, endedVisibleDays int) ([]meetRow, int, error) {
	args := []any{uid}
	// hidden_by_owner 在探索列表一律排除，沒有例外（包含發起人自己瀏覽時）——「隱藏」的產品定義
	// 就是「從入口關閉」，發起人要管理自己隱藏的團練，走的是 Mine()/owned 分頁，不是這裡。
	where := []string{"m.deleted_at IS NULL", "m.hidden_by_admin = FALSE", "m.hidden_by_owner = FALSE"}

	if f.Ended {
		// 已結束折疊區：meet_at 已過，且還在保留天數內（超過就從探索消失，資料不刪）
		// ⚠️ 用 make_interval(days => $n) 而不是 ($n || ' days')::interval：
		// pgx 會把參數當 int4 送出，而 Postgres 沒有 integer || text 的運算子，
		// 後者會在執行期炸成 42883（operator does not exist）。
		args = append(args, endedVisibleDays)
		where = append(where, "m.meet_at <= NOW()",
			fmt.Sprintf("m.meet_at > NOW() - make_interval(days => $%d)", len(args)))
	} else {
		where = append(where, "m.meet_at > NOW()", "m.status = 'open'")
	}

	if q := strings.TrimSpace(f.Q); q != "" {
		args = append(args, "%"+q+"%")
		i := len(args)
		where = append(where, fmt.Sprintf("(m.title ILIKE $%d OR m.place_label ILIKE $%d OR m.region ILIKE $%d)", i, i, i))
	}
	if reg := strings.TrimSpace(f.Region); reg != "" {
		args = append(args, reg)
		where = append(where, fmt.Sprintf("m.region = $%d", len(args)))
	}
	switch f.Privacy {
	case "public":
		where = append(where, "m.join_password_hash IS NULL")
	case "private":
		where = append(where, "m.join_password_hash IS NOT NULL")
	}
	switch f.Approval {
	case "free":
		where = append(where, "m.approval_required = FALSE")
	case "review":
		where = append(where, "m.approval_required = TRUE")
	}
	if f.HasSlot {
		where = append(where, "m.member_count < m.capacity")
	}

	near := f.NearLat != nil && f.NearLng != nil
	if near {
		// ⚠️ 半徑一律吸附到 band 邊界（handler 已做過一次，這裡是最後一道保險：
		// 任何呼叫路徑都不得讓連續可控的半徑進到距離比較裡，見 geo.go snapRadiusKm）。
		f.RadiusKm = snapRadiusKm(f.RadiusKm)
		// bounding box 多留一格量化誤差，避免 snapCoord 把邊界附近的團練推出粗篩範圍。
		minLat, maxLat, minLng, maxLng := boundingBox(*f.NearLat, *f.NearLng, f.RadiusKm+geoCellMeters/1000)
		args = append(args, minLat, maxLat, minLng, maxLng)
		i := len(args)
		// ⚠️ m.lat IS NOT NULL 順帶把 no_location=TRUE 的團排除在附近搜尋之外——這些團沒有座標
		// 可比對距離（migration 161 的 CHECK 保證 no_location 恆無座標），被濾掉是正確行為，
		// 不是漏洞：使用者選了「不限地點」，本來就沒有「附近」這個概念可言，不需要另外特判。
		where = append(where, "m.lat IS NOT NULL",
			fmt.Sprintf("m.lat BETWEEN $%d AND $%d", i-3, i-2),
			fmt.Sprintf("m.lng BETWEEN $%d AND $%d", i-1, i))
	}

	whereSQL := " WHERE " + strings.Join(where, " AND ")

	if near {
		// 粗篩候選 → Go 端 haversine 精算 → 依距離排序 → 分頁。
		rows, err := r.db.Query(ctx, `SELECT `+meetCols+meetJoins+whereSQL+
			fmt.Sprintf(" ORDER BY m.meet_at ASC LIMIT %d", nearCandidateCap), args...)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		var all []meetRow
		for rows.Next() {
			m, err := scanMeet(rows)
			if err != nil {
				return nil, 0, err
			}
			if m.Lat == nil || m.Lng == nil {
				continue
			}
			// ⚠️ 距離一律用**量化過**的座標算（geo.go snapCoord）。直接用真值算會讓
			// 「有沒有被篩掉」＋「落在哪個 band」變成可三角定位的神諭——band 邊界是以
			// 查詢者自選座標為圓心的精確圓，移動三次查詢座標就能解出精確集合點。
			// 量化之後，攻擊者最多只能還原出格點中心（真值仍散布在 500 m 格內）。
			sLat, sLng := snapCoord(*m.Lat, *m.Lng, m.ID)
			d := haversineM(*f.NearLat, *f.NearLng, sLat, sLng)
			// 半徑已吸附到 band 邊界，所以這個比較等價於「band 在允許集合內」——
			// 過濾條件不會比回應中的 distance_band 多洩漏任何資訊。
			if d >= f.RadiusKm*1000 {
				continue // bounding box 是矩形，角落會超出半徑，這裡剃掉
			}
			m.distanceM = d
			all = append(all, m)
		}
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].distanceM < all[j].distanceM })
		total := len(all)
		lo := f.Offset
		if lo > total {
			lo = total
		}
		hi := lo + f.Limit
		if hi > total {
			hi = total
		}
		return all[lo:hi], total, nil
	}

	order := " ORDER BY m.meet_at ASC"
	switch f.Sort {
	case "new":
		order = " ORDER BY m.created_at DESC"
	case "hot":
		order = " ORDER BY (m.reaction_count * 2 + m.comment_count) DESC, m.meet_at ASC"
	}
	if f.Ended {
		order = " ORDER BY m.meet_at DESC"
	}

	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*)`+meetJoins+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, f.Limit, f.Offset)
	rows, err := r.db.Query(ctx, `SELECT `+meetCols+meetJoins+whereSQL+order+
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []meetRow
	for rows.Next() {
		m, err := scanMeet(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

// GetMeet 單筆（前台視角）。軟刪／後台下架一律當作不存在（404，不外洩差異）。
//
// ⚠️ hidden_by_owner 的可見性例外：發起人自己、已加入成員（mm.status='joined'）仍可查看——
// 否則已成團的人會突然看不到集合資訊（規格明訂）。申請中（pending）不算數：隱藏的產品定義
// 是「只剩發起人和管理者看得到」，pending 屬於「其他人」。這支同時是 Join 端點的第一道閘門
// （handler.Join 呼叫 GetMeet 失敗就直接 404，走不到 repository.Join），Join() 內部另有
// 第二道（見 members.go 的 hidden 檢查），兩層互為防線。
func (r *Repository) GetMeet(ctx context.Context, uid, id string) (meetRow, error) {
	row := r.db.QueryRow(ctx, `SELECT `+meetCols+meetJoins+
		` WHERE m.id = $2 AND m.deleted_at IS NULL AND m.hidden_by_admin = FALSE
		    AND (m.hidden_by_owner = FALSE OR m.owner_id = $1 OR mm.status = 'joined')`, uid, id)
	m, err := scanMeet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	return m, err
}

// IsDeleted 只給 Detail 端點用：分辨「已軟刪」與其餘讓 GetMeet 找不到列的原因（不存在／
// 後台下架／發起人隱藏且非當事人）。只有「已刪除」這一種可以額外揭露成 410（規格理由：
// 連結是發起人主動分享出去的，收到連結的人本來就知道它存在過，不構成新洩漏；其餘原因
// GetMeet 仍統一回 404，不外洩差異——同一套理由見 share.go 檔頭）。
// 刻意獨立於 GetMeet 之外：不想讓 Update/Join/Members 等其他呼叫端也跟著換行為或回應碼。
func (r *Repository) IsDeleted(ctx context.Context, id string) (bool, error) {
	var deleted bool
	err := r.db.QueryRow(ctx, `SELECT deleted_at IS NOT NULL FROM run_meets WHERE id=$1`, id).Scan(&deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return deleted, err
}

// GetMeetAdmin 單筆（後台視角）：含已下架、已軟刪、發起人已隱藏——**刻意不加任何 hidden_by_*
// 過濾**。後台需要無條件看到一切才能處理檢舉/糾紛，這條路徑本來就沒有 hidden_by_admin 過濾
// （見既有實作），hidden_by_owner 比照辦理，不需要另外加白名單條件。
func (r *Repository) GetMeetAdmin(ctx context.Context, viewerID, id string) (meetRow, error) {
	row := r.db.QueryRow(ctx, `SELECT `+meetCols+meetJoins+` WHERE m.id = $2`, viewerID, id)
	m, err := scanMeet(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, errNotFound
	}
	return m, err
}

// Mine 我的團練三段：我發起的 / 我參加的 / 申請中。
// 已結束的仍然看得到（規格 5.6：從探索折疊區消失後，成員仍可在「我的」看到）。
//
// ⚠️ 刻意不加 hidden_by_owner 過濾：這整支查詢本來就是「當事人可見」——owned 段是
// owner_id=$1（發起人本人管理隱藏團練正是走這裡，不是探索列表）；joined 段是
// mm.status='joined'（規格明訂已加入成員不受隱藏影響）；pending 段雖不在規格的例外清單裡，
// 但這是使用者自己申請中的清單，讓自己的申請從自己的「我的」頁面消失沒有任何隱私或產品
// 上的理由——加了 hidden_by_owner 過濾只會製造「申請明明還在、卻突然從清單消失」的錯覺。
func (r *Repository) Mine(ctx context.Context, uid string) (owned, joined, pending []meetRow, err error) {
	load := func(cond string) ([]meetRow, error) {
		rows, e := r.db.Query(ctx, `SELECT `+meetCols+meetJoins+
			` WHERE m.deleted_at IS NULL AND m.hidden_by_admin = FALSE AND `+cond+
			` ORDER BY m.meet_at DESC LIMIT 100`, uid)
		if e != nil {
			return nil, e
		}
		defer rows.Close()
		var out []meetRow
		for rows.Next() {
			m, e := scanMeet(rows)
			if e != nil {
				return nil, e
			}
			out = append(out, m)
		}
		return out, rows.Err()
	}
	if owned, err = load(`m.owner_id = $1`); err != nil {
		return
	}
	if joined, err = load(`m.owner_id <> $1 AND mm.status = 'joined'`); err != nil {
		return
	}
	pending, err = load(`m.owner_id <> $1 AND mm.status = 'pending'`)
	return
}

// --- 編輯／狀態 ---

// UpdateResult 編輯後要回報給前端的附帶資訊。
type UpdateResult struct {
	PendingKept    int  // approval_required TRUE→FALSE 時仍未處理的申請數（規格 1.5：不自動通過）
	TimeOrPlaceChg bool // 時間或地點有變（P2 站內信只在這種變更時發）
}

// UpdateMeet 編輯（僅發起人）。在 FOR UPDATE 交易內檢查「新上限不得低於現有成員數」——
// 刻意不加 DB CHECK (member_count <= capacity)：那會讓舊資料或正常 UPDATE 整筆炸掉。
func (r *Repository) UpdateMeet(ctx context.Context, uid, id string, in *MeetInput, effectiveLimit int) (UpdateResult, error) {
	var res UpdateResult
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	// ⚠️ 這裡的 FOR UPDATE 查詢刻意**不加 hidden_by_owner 過濾**：與 GetMeet（前台瀏覽用、要在
	// SQL 層擋非當事人）不同，這支只服務「編輯」這一個動作，緊接著就用 Go 層的
	// `ownerID != uid` 擋掉所有非本人——本人一定要能編輯自己隱藏中的團練（隱藏是「不給別人看」，
	// 不是「連自己都不能改」），加了 hidden_by_owner 條件反而會誤傷這個情境。
	// hidden_by_admin 仍然過濾：後台下架與發起人自己的動作無關，下架期間本來就不該讓任何人
	// （含發起人）繞過後台審核逕自編輯內容。
	var ownerID, status string
	var memberCount, pendingCount, imageLimit int
	var oldApproval bool
	var oldMeetAt time.Time
	var oldRegion, oldPlace, oldDetail string
	var oldLat, oldLng *float64
	err = tx.QueryRow(ctx, `
		SELECT owner_id, status, member_count, pending_count, image_limit, approval_required,
		       meet_at, region, place_label, meeting_detail, lat, lng
		  FROM run_meets
		 WHERE id=$1 AND deleted_at IS NULL AND hidden_by_admin = FALSE
		 FOR UPDATE`, id).
		Scan(&ownerID, &status, &memberCount, &pendingCount, &imageLimit, &oldApproval,
			&oldMeetAt, &oldRegion, &oldPlace, &oldDetail, &oldLat, &oldLng)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, errNotFound
	}
	if err != nil {
		return res, err
	}
	if ownerID != uid {
		return res, errNotOwner
	}
	if !CanEdit(status) {
		return res, meetStatusError(status, false)
	}
	// ⚠️ 已過期的團一律不得編輯。過期不改 status 是刻意的（規格 5.6 用查詢條件判定），
	// 但這也代表 status 仍是 'open'——若放行，團主可以把一個早就結束的團的 title/地點/說明
	// 全部換掉、meet_at 推到下週，等於用同一次配額無限產出「新團練」，架空
	// 「非 VIP 每月 1 次」這個最強的 VIP 轉換鉤子（規格 1.2）。
	// 過期前改期（固定團練每週順延）仍然可以，那是刻意支援的情境。
	if !oldMeetAt.After(time.Now()) {
		return res, errEditEnded
	}
	if in.Capacity < memberCount {
		return res, errCapacityBelowMembers(memberCount)
	}
	// 上限＝max(建立當下快照, handler 依現行設定與 VIP 身分算出的 effectiveLimit)，
	// 見 quota.go EffectiveImageLimit：快照保底（VIP 到期仍能編輯既有多圖團）、
	// 現行上限跟進（後台調高後既有團練也放寬）。effectiveLimit<=0 代表呼叫端沒給，退回只用快照。
	limit := imageLimit
	if effectiveLimit > limit {
		limit = effectiveLimit
	}
	if len(in.ImageURLs) > limit {
		return res, errImageOverLimit(limit, 0) // repository 拿不到設定 → 不做升級引導
	}

	// 密碼三態：nil＝不動 / ""＝移除（改公開）/ 其他＝重設
	passwordSQL := "join_password_hash"
	args := []any{id, in.Title, in.MeetAt, in.Region, in.PlaceLabel, in.Lat, in.Lng,
		in.MeetingDetail, in.Capacity, in.Description, in.ImageURLs, in.ApprovalRequired, in.NoLocation}
	if in.Password != nil {
		if *in.Password == "" {
			passwordSQL = "NULL"
		} else {
			h, e := bcrypt.GenerateFromPassword([]byte(*in.Password), bcrypt.DefaultCost)
			if e != nil {
				return res, e
			}
			args = append(args, string(h))
			passwordSQL = fmt.Sprintf("$%d", len(args))
		}
	}

	if _, err = tx.Exec(ctx, `
		UPDATE run_meets
		   SET title=$2, meet_at=$3, region=$4, place_label=$5, lat=$6, lng=$7,
		       meeting_detail=$8, capacity=$9, description=$10, image_urls=$11,
		       approval_required=$12, no_location=$13, join_password_hash=`+passwordSQL+`, updated_at=NOW()
		 WHERE id=$1`, args...); err != nil {
		return res, err
	}

	// 密碼變更的撤銷語意（規格 1.4）：重設或移除密碼 → 清空解鎖票證。
	// 已 joined/pending 的成員不受影響——成員身分本身就是 access。
	if in.Password != nil {
		if _, err = tx.Exec(ctx, `DELETE FROM run_meet_access WHERE meet_id=$1`, id); err != nil {
			return res, err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return res, err
	}

	if oldApproval && !in.ApprovalRequired {
		res.PendingKept = pendingCount // 不自動通過，只提示發起人手動處理
	}
	res.TimeOrPlaceChg = !oldMeetAt.Equal(in.MeetAt) || oldRegion != in.Region ||
		oldPlace != in.PlaceLabel || oldDetail != in.MeetingDetail ||
		!sameCoord(oldLat, in.Lat) || !sameCoord(oldLng, in.Lng)
	return res, nil
}

func sameCoord(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// SetStatus 切換 open/closed/cancelled（僅發起人）。合法轉換表見 model.go CanTransition：
//
//	open→closed（關閉，可逆）／closed→open（重新開啟）／open→cancelled／closed→cancelled（中止）／
//	cancelled→open（恢復）。不合法轉換（例如 cancelled→closed、同狀態互轉）回 errBadTransition，
//	不外洩目前確切狀態——呼叫端自己指定要轉去哪裡，只需要知道「不行」。
//
// 回傳被一併婉拒的待審申請 user_id（只有轉為 cancelled 才會非空）：
//
//	⚠️ closed 是可逆的「不再收新人，其他功能照舊」（使用者定案），待審申請一律保留，
//	重開後發起人可以繼續處理——這是本次重整的主因，原本關閉會無條件婉拒所有待審，
//	那是為「不可逆」設計的收尾，現在改由 cancelled 承擔。
//	cancelled 才是「停止加入的任何動作」，此時才把所有待審一併婉拒（沿用原本 closed 那段
//	交易邏輯）：不做的話會卡出一個無法離開的死迴圈——pending_count 不歸零 → 詳情頁一直顯示
//	「有 N 筆待審核申請，前往處理」→ 發起人按同意卻被 Approve 的 status != open 檢查擋成
//	409 → 申請人那端 CTA 也永遠停在「審核中…」。
//
// ⚠️ 這條路徑**完全不碰 run_meet_used**——「開啟後關閉一樣消耗一次」（見 quota.go 檔頭）。
func (r *Repository) SetStatus(ctx context.Context, uid, id, status string) ([]string, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// ⚠️ 這裡沒有 hidden_by_owner 過濾：WHERE 已經直接帶 owner_id=$2，本來就是「當事人可見」
	// 查詢——切換自己團練的狀態這件事，跟自己有沒有把它隱藏起來無關，加了只會誤傷發起人本人。
	var cur string
	err = tx.QueryRow(ctx, `
		SELECT status FROM run_meets
		 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL AND hidden_by_admin=FALSE
		 FOR UPDATE`, id, uid).Scan(&cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound // 不存在／非本人／已下架，一律不外洩差異
	}
	if err != nil {
		return nil, err
	}
	if !CanTransition(cur, status) {
		return nil, errBadTransition
	}

	// closed_at/closed_by 只在進入 closed/cancelled 時寫；回到 open 時清成 NULL——可逆語意下，
	// 重開的團不該留著上一輪關閉的時間戳／操作者（前端若拿它顯示「已關閉多久」會被誤導成
	// 從沒開過一樣久）。
	var closedAt *time.Time
	var closedBy *string
	if status != StatusOpen {
		now := time.Now()
		closedAt, closedBy = &now, &uid
	}
	if _, err = tx.Exec(ctx, `
		UPDATE run_meets SET status=$2, closed_at=$3, closed_by=$4, updated_at=NOW()
		 WHERE id=$1`, id, status, closedAt, closedBy); err != nil {
		return nil, err
	}

	if status != StatusCancelled {
		// closed（可逆）／open（重開）都不動待審申請：closed 保留給重開後繼續處理；
		// open 本來就沒有「因為轉態而該婉拒」的申請（唯一會累積 pending 的來源是加入申請本身）。
		return nil, tx.Commit(ctx)
	}

	// 中止：一併婉拒所有待審申請（decided_by 記發起人；婉拒冷卻讀 decided_at）。
	// 回傳被婉拒者的 user_id 供 handler 推播——他們已不在 joined/pending 名單裡，
	// notifyMembers 撈不到，不主動推的話 CTA 會一直停在「⏳ 審核中…」。
	rows, err := tx.Query(ctx, `
		UPDATE run_meet_members SET status='rejected', decided_at=NOW(), decided_by=$2
		 WHERE meet_id=$1 AND status='pending'
		RETURNING user_id`, id, uid)
	if err != nil {
		return nil, err
	}
	var rejected []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return nil, err
		}
		rejected = append(rejected, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(rejected) > 0 {
		if _, err = tx.Exec(ctx, `UPDATE run_meets SET pending_count=0 WHERE id=$1`, id); err != nil {
			return nil, err
		}
	}
	return rejected, tx.Commit(ctx)
}

// SetVisibility 發起人自行隱藏／取消隱藏（hidden_by_owner，可逆）。
// ⚠️ 與 hidden_by_admin 分離：這支只能動 hidden_by_owner 這一欄，發起人無法透過它解除
// 後台下架（WHERE 排除 hidden_by_admin=TRUE，一併下架期間前台任何端點對該團一律 404，
// 這支也不例外——與既有 GetMeet/lockMeet 的行為一致，不開特例）。
func (r *Repository) SetVisibility(ctx context.Context, uid, id string, hidden bool) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE run_meets SET hidden_by_owner=$3, updated_at=NOW()
		 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL AND hidden_by_admin=FALSE`,
		id, uid, hidden)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound // 不存在／非本人／已下架，一律不外洩差異
	}
	return nil
}

// SoftDelete 發起人軟刪除。⚠️ 同樣不回補配額。
func (r *Repository) SoftDelete(ctx context.Context, uid, id string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE run_meets SET deleted_at=NOW(), updated_at=NOW()
		 WHERE id=$1 AND owner_id=$2 AND deleted_at IS NULL`, id, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}

// --- 私密團密碼 ---

// dummyHash 固定的 bcrypt hash（明碼是隨機字串，永不會被猜中）。
// 用途：團不存在／已下架時仍然跑一次 CompareHashAndPassword，消除「存在與否」的時序差
// （規格 1.4 防暴力破解第 3 層：統一錯誤 ＋ 統一延遲）。
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// VerifyPassword 驗證私密團密碼。回 (ok, isPrivate, err)。
// 團不存在／非私密團一律走 dummy 比對後回 ok=false，呼叫端統一回同一句 403。
//
// ⚠️ hidden_by_owner=FALSE 這裡無條件加、不留當事人例外：Unlock 這支動作的意義是「讓一個
// 還沒有存取權的訪客拿到詳情頁入場券」，發起人與已加入成員本來就不需要（也不會）呼叫這支
// ——HasDetailAccess 對 isOwner/joined 早就直接放行。所以會打這支端點的人，一定是還沒有
// 任何關係的訪客；隱藏團練的定義正是「這種人看不到」，讓密碼正確與否都無法解鎖一個隱藏的團，
// 才是「隱藏」這個詞該有的效果——否則知道連結＋猜中密碼就能繞過隱藏，隱藏就形同虛設。
func (r *Repository) VerifyPassword(ctx context.Context, id, password string) (bool, error) {
	var hash *string
	err := r.db.QueryRow(ctx, `
		SELECT join_password_hash FROM run_meets
		 WHERE id=$1 AND deleted_at IS NULL AND hidden_by_admin=FALSE AND hidden_by_owner=FALSE`, id).Scan(&hash)
	if err != nil || hash == nil || *hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) // 統一時序
		return false, nil
	}
	if bcrypt.CompareHashAndPassword([]byte(*hash), []byte(password)) != nil {
		return false, nil
	}
	return true, nil
}

// GrantAccess 寫入解鎖票證（一次解鎖、跨裝置有效）。
// ⚠️ 通過密碼 ≠ 成為成員：這張票只讓人看到完整說明與圖片，精確地點仍需正式加入。
func (r *Repository) GrantAccess(ctx context.Context, meetID, uid string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO run_meet_access (meet_id, user_id) VALUES ($1,$2)
		ON CONFLICT DO NOTHING`, meetID, uid)
	return err
}

// --- 熱門跑點快選（建立表單用）---

// PlaceSuggest 從既有 explore_bosses 找地點建議。
//
// ⚠️ 只回 region/place/lat/lng 四個欄位。探索系統有「未揭露關主要遮蔽身分/圖片/課表」的既有
// 規則（explore.maskBoss）；這裡若順手多回 name/code/scene_image_url/segments，等於開一條
// 繞過該規則的旁路。上限 20 筆。
func (r *Repository) PlaceSuggest(ctx context.Context, q string, lat, lng *float64) ([]PlaceSuggestion, error) {
	var rows pgx.Rows
	var err error
	if q = strings.TrimSpace(q); q != "" {
		rows, err = r.db.Query(ctx, `
			SELECT region, place, lat, lng FROM explore_bosses
			 WHERE enabled AND region <> '' AND place <> '' AND NOT (lat = 0 AND lng = 0)
			   AND (place ILIKE $1 OR region ILIKE $1)
			 ORDER BY place LIMIT 20`, "%"+q+"%")
	} else if lat != nil && lng != nil {
		// 依座標找最近的 20 個（平面近似排序即可——這裡只是表單建議值，不是判定邏輯）
		rows, err = r.db.Query(ctx, `
			SELECT region, place, lat, lng FROM explore_bosses
			 WHERE enabled AND region <> '' AND place <> '' AND NOT (lat = 0 AND lng = 0)
			 ORDER BY ((lat-$1)*(lat-$1) + (lng-$2)*(lng-$2)) ASC LIMIT 20`, *lat, *lng)
	} else {
		return []PlaceSuggestion{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlaceSuggestion{}
	for rows.Next() {
		var p PlaceSuggestion
		if err := rows.Scan(&p.Region, &p.Place, &p.Lat, &p.Lng); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- 檢舉（最小版）---

func (r *Repository) CreateReport(ctx context.Context, meetID string, commentID *string, reporterID, reason string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO run_meet_reports (meet_id, comment_id, reporter_id, reason)
		VALUES ($1,$2,$3,$4)`, meetID, commentID, reporterID, reason)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return nil // 每人每個對象只能檢舉一次；重複視為成功（冪等，不告訴對方已檢舉過）
	}
	return err
}
