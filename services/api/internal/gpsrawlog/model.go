// Package gpsrawlog：GPS 原始定位點記錄（除錯用，見 docs/gps/GPS_START_GATE_RAWLOG_CONTRACT.md 契約 B）。
//
// 背景（2026-09-29 owner 拍板）：當天實跑 App 5.44 km vs COROS 5.307 km，懷疑 App GPS 精度問題，
// 需要保存「按下開始那一刻起」的原始定位點序列（含未被採納/被排除的點與其分類碼）供離線重播分析。
// 只對使用者本人帳號（白名單，見 Allowed／app_settings gps_raw_log_whitelist）保存，30 天自動清除
// （見 PurgeExpired，掛在既有每日報告排程），只有後台超管看得到（無 super_admin 旁路寫入）。
//
// 獨立成一個套件（比照 internal/gpscalib 前例）：讀寫 gps_run_raw_points 不需要牽動
// internal/activity 既有的距離重算/防弊/校正管線，保持那個套件的既有職責單純。
package gpsrawlog

import (
	"encoding/json"
	"fmt"
	"time"
)

// maxRows／rowFields：POST /me/gps-runs/{runId}/raw-points 的欄位驗證上限（見契約 B「後端」段：
// 「rows ≤ 30,000」），與前端記憶體緩衝上限 30,000 列（見契約 B「前端收集」段）一致——伺服器端
// 仍需獨立驗證，不能只信任前端已經擋過。body 3 MB 上限在 handler 層用 http.MaxBytesReader 擋
// （見 handler.go maxUploadBodyBytes），不在這裡重複定義。
const (
	maxRows   = 30000
	rowFields = 7 // [t_ms, lat, lng, acc, speed|null, heading|null, code]
)

// validCodes 前端 onPos 分支標記（見契約 B「前端收集」段逐字列舉）：a 採納計入／j 未達
// JITTER_MIN 略過／p 精度差／x 超速或斷訊排除／h 靜止暫存／d 暫存後丟棄／f 起點；'?' 是「做不到
// 精準分支」的保留值，一併放行——後端只驗證碼落在已知集合內，不對「這趟碼分佈合不合理」做判斷
// （那是離線分析的事，見契約 C 驗收段「code 分佈合理」交由人工核對）。
var validCodes = map[string]bool{
	"a": true, "j": true, "p": true, "x": true, "h": true, "d": true, "f": true, "?": true,
}

// UploadPayload 前端 POST body（見契約 B「前端收集」段最後一句：
// body {"v":1,"fields":[...],"rows":[...],"truncated":bool,"client_version":...}）。Rows 用
// [][]any 而非具名 struct——欄位型別混合（數字／null／單字元字串）用 JSON array 逐列傳輸最省
// bytes（契約明講「緩衝只放記憶體、上限 30,000 列」，除錯用途不追求可讀性），驗證邏輯見 validateRow。
type UploadPayload struct {
	V             int      `json:"v"`
	Fields        []string `json:"fields"`
	Rows          [][]any  `json:"rows"`
	Truncated     bool     `json:"truncated"`
	ClientVersion string   `json:"client_version"`
}

// storedPoints 實際寫進 gps_run_raw_points.points 的 JSONB 內容——比 UploadPayload 少一個
// ClientVersion（那欄已獨立存 gps_run_raw_points.client_version 欄位，見 migration 195 註解，
// 不在 JSONB 內重複一份，避免兩處不同步）。
type storedPoints struct {
	V         int      `json:"v"`
	Fields    []string `json:"fields"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}

// Record GET /api/v1/admin/gps-runs/{runId}/raw-points 回應（見契約 B「後端」段：
// 「回傳原始 JSON（附 run 基本資訊：distance_km、duration_s、km_paces、client_version）」）。
type Record struct {
	RunID         string          `json:"run_id"`
	PointCount    int             `json:"point_count"`
	Points        json.RawMessage `json:"points"`                   // 原封不動的 storedPoints JSON（{"v","fields","rows","truncated"}）
	ClientVersion string          `json:"client_version,omitempty"` // 上傳原始定位點當下的前端版號
	CreatedAt     time.Time       `json:"created_at"`
	Run           RunSummary      `json:"run"`
}

// RunSummary 附帶的 run 基本資訊（見 Record 註解）。
type RunSummary struct {
	DistanceKm    float64 `json:"distance_km"`
	DurationS     int     `json:"duration_s"`
	KmPaces       []int   `json:"km_paces,omitempty"`
	ClientVersion string  `json:"client_version,omitempty"` // gps_runs.client_version（跑步上傳當下的版號，可能跟上面那個不是同一次）
}

// validateRow 驗證單一列 [t_ms, lat, lng, acc, speed|null, heading|null, code]（見契約 B「前端收集」段）。
// 只做結構與合理範圍檢查（不假設前端一定照順序帶對——欄位數不對／型別不符／範圍離譜一律拒絕整批，
// 不做「部分接受、忽略壞列」，避免存進一批看似完整、其實混雜垃圾資料的除錯紀錄）。JSON 數字解碼
// 後一律是 float64（encoding/json 預設行為），型別斷言用這個。
func validateRow(row []any) error {
	if len(row) != rowFields {
		return fmt.Errorf("列欄位數須為 %d，實得 %d", rowFields, len(row))
	}
	if tMs, ok := row[0].(float64); !ok || tMs < 0 {
		return fmt.Errorf("t_ms 須為非負數")
	}
	lat, ok := row[1].(float64)
	if !ok || lat < -90 || lat > 90 {
		return fmt.Errorf("lat 超出範圍")
	}
	lng, ok := row[2].(float64)
	if !ok || lng < -180 || lng > 180 {
		return fmt.Errorf("lng 超出範圍")
	}
	if acc, ok := row[3].(float64); !ok || acc < 0 {
		return fmt.Errorf("acc 須 >= 0")
	}
	if row[4] != nil {
		if _, ok := row[4].(float64); !ok {
			return fmt.Errorf("speed 型別錯誤")
		}
	}
	if row[5] != nil {
		if _, ok := row[5].(float64); !ok {
			return fmt.Errorf("heading 型別錯誤")
		}
	}
	code, ok := row[6].(string)
	if !ok || !validCodes[code] {
		return fmt.Errorf("code 不合法")
	}
	return nil
}

// Validate 驗證整包上傳（見 handler.go UploadRawPoints 對 http.MaxBytesReader 的呼叫——3 MB body
// 上限在那一層擋，這裡只驗證解碼後的結構與筆數）。
func (p UploadPayload) Validate() error {
	if p.V != 1 {
		return fmt.Errorf("不支援的版本")
	}
	if len(p.Rows) > maxRows {
		return fmt.Errorf("列數超過上限 %d", maxRows)
	}
	for i, row := range p.Rows {
		if err := validateRow(row); err != nil {
			return fmt.Errorf("第 %d 列：%w", i, err)
		}
	}
	return nil
}
