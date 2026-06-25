.PHONY: dev up down migrate seed setup reset logs test-api load-test web admin

# 啟動本地開發環境（DB + Redis + PgBouncer）
up:
	docker compose up -d postgres redis pgbouncer

# 啟動全部服務（含 API + Worker）
dev:
	docker compose up -d

# 停止所有服務
down:
	docker compose down

# 執行資料庫 migration
migrate:
	cd services/api && go run ./cmd/migrate/main.go up

# 初始化種子資料（admin + 測試賽事）
seed:
	docker compose exec -T postgres psql -U dor -d dor < scripts/seed.sql

# 一鍵初始化（首次使用）：啟動 → 等待 → migrate → seed
setup:
	docker compose up -d postgres redis pgbouncer
	@echo "等待 PostgreSQL 就緒..."
	@sleep 5
	make migrate
	make seed
	@echo "✓ 初始化完成，執行 'make dev' 啟動全部服務"

# 重置資料庫（危險：清除所有資料）
reset:
	docker compose down -v
	docker compose up -d postgres redis pgbouncer
	@sleep 5
	make migrate
	make seed

# 查看日誌
logs:
	docker compose logs -f api worker

# 啟動 API（本地，不用 Docker）
run-api:
	cd services/api && go run ./cmd/api/main.go

# 啟動 Worker（本地，不用 Docker）
run-worker:
	cd services/worker && go run ./main.go

# 執行 API 測試
test-api:
	cd services/api && go test ./... -v

# 壓力測試（需安裝 k6）
load-test:
	k6 run tests/load/race_ws.js

# 前台開發
web:
	cd apps/web && npm run dev

# 後台開發
admin:
	cd apps/admin && npm run dev
