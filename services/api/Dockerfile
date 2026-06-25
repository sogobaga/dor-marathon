# 生產環境 Dockerfile — 多階段建構，最小化映像大小
FROM golang:1.22-alpine AS builder

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
