package middleware

import (
	"net/http"
	"strings"
)

// MaxBodyBytes 全域 request body 大小上限（SEC-M4），避免超大 JSON payload 撐爆記憶體。
//
// skipPrefixes 列出的路徑前綴不受此全域上限限制。目前用來排開：
//   - 圖片/音檔上傳端點（/api/v1/admin/images、/api/v1/profile/avatar）——它們各自已有自己的
//     5MB http.MaxBytesReader（見 internal/image.Upload）。http.MaxBytesReader 疊加時是「先讀到
//     的那層先擋」，不是取兩層中較大的上限，所以若不排除，這裡的全域上限會先擋下正常的圖片上傳。
//   - 大型 JSON 匯入端點（/api/v1/admin/personal-tasks/import）——前台 SheetJS 把整份 xlsx
//     課表轉成 JSON 一次送出（見 internal/personaltask.Handler.Import），計畫/任務筆數多時
//     整包容易 >1MB，會被全域上限誤擋成 413；此端點已在 RequireAdmin + perm("event_tasks")
//     之後，非公開端點，放寬上限風險可控。之後若有其他 admin 匯入端點（xlsx/bulk 類）
//     也超過 1MB，一併加進這裡，不要放寬全域上限本身。
func MaxBodyBytes(limit int64, skipPrefixes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range skipPrefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
