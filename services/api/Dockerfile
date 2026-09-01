# 生產環境 Dockerfile — 多階段建構，最小化映像大小
# 基底映像走 AWS ECR Public 的 Docker 官方鏡像（內容/tag 與 Docker Hub 完全相同）：
# 2026-09-01 Docker Hub registry 間歇性 500 連續打掛兩次部署（build 3 秒即死），
# ECR Public 由 AWS 託管、不受 Docker Hub 故障影響。distroless 本來就在 gcr.io，不受影響。
FROM public.ecr.aws/docker/library/golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /dor-api ./cmd/api/main.go

# 最終映像：distroless（無 shell，攻擊面最小）
FROM gcr.io/distroless/static-debian12

COPY --from=builder /dor-api /dor-api

EXPOSE 8080
ENTRYPOINT ["/dor-api"]
