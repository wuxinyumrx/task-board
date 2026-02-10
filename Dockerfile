## ---- 构建阶段：编译 Go 二进制 ----
FROM golang:1.22-alpine AS builder
WORKDIR /app
RUN apk add --no-cache build-base
COPY go.mod ./
# 如果有依赖，取消下一行注释以提前下载依赖
# RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o server .

## ---- 运行阶段：精简镜像运行服务 ----
FROM alpine:3.19
WORKDIR /app
COPY --from=builder /app/server /app/server
COPY --from=builder /app/web /app/web
RUN mkdir -p /app/data && adduser -D -u 10001 appuser && chown -R appuser:appuser /app
ENV PORT=8080
EXPOSE 8080
USER appuser
CMD ["/app/server"]
