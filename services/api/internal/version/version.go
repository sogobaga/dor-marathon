// Package version 提供應用版號：v<Base>.<commit8>。
// Base 為主版號（v0.0 = 開發中）；commit 取自部署平台注入的 git SHA（Railway）。
package version

import "os"

// Base 主版號。正式發布時手動進版（同步調整前端 next.config.mjs 的 VERSION_BASE）。
const Base = "0.0"

// Commit 取得目前部署的 git commit 短碼（Railway 於 runtime 注入 RAILWAY_GIT_COMMIT_SHA）。
func Commit() string {
	for _, k := range []string{"RAILWAY_GIT_COMMIT_SHA", "GIT_COMMIT", "SOURCE_COMMIT"} {
		if c := os.Getenv(k); c != "" {
			if len(c) > 8 {
				return c[:8]
			}
			return c
		}
	}
	return "dev"
}

// Full 完整版號，例：v0.0.cd87ee7c
func Full() string { return "v" + Base + "." + Commit() }
