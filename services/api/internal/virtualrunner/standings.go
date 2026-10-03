package virtualrunner

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RecomputeStandingsForUsers 對「這批 userIDs 有報名」的競賽模式賽事重算 race_group_standings
// （同 recomputeStandingsSQL，與 services/worker/main.go aggregateStandings 逐字同步）。
//
// 匯出給 cmd/api/main.go 注入 integration.SetCompetitionRecompute：外部來源（COROS MCP、Garmin 直連…）匯入
// 活動後不經 Redis stream、worker 不會被觸發，競賽分組成績（預聚合表）不重算就不會反映這些里程
// （COROS GA 契約 §3.5）。放在這個套件而不是讓 integration 自己複製一份 SQL，是為了維持「同一段聚合 SQL
// 只有 api 端一份」。
func RecomputeStandingsForUsers(ctx context.Context, db *pgxpool.Pool, userIDs []string) error {
	return NewRepository(db).recomputeStandingsForUsers(ctx, userIDs)
}
