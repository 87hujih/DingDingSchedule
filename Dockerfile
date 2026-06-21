# 多阶段构建：减小最终镜像体积
# 阶段1: 构建阶段
FROM golang:1.24-alpine AS builder

# 设置工作目录
WORKDIR /build

# 可选切换 Alpine 镜像源；CI 默认使用官方源，国内构建可传
# --build-arg ALPINE_MIRROR=https://mirrors.aliyun.com 加速。
ARG ALPINE_MIRROR=
RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s#https://dl-cdn.alpinelinux.org#${ALPINE_MIRROR%/}#g" /etc/apk/repositories; \
    fi

# 安装必要的构建工具
RUN apk add --no-cache git gcc musl-dev

# Go 代理同样允许按构建环境覆盖；GitHub Actions 默认使用官方代理。
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

# 复制 go.mod 和 go.sum 就并下载依赖（利用 Docker 缓存）
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 编译应用（静态链接，禁用 CGO 以减小体积）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o schedule_server ./cmd/main.go

# 阶段2: 运行阶段
FROM alpine:latest

# 可选切换 Alpine 镜像源。
ARG ALPINE_MIRROR=
RUN if [ -n "$ALPINE_MIRROR" ]; then \
      sed -i "s#https://dl-cdn.alpinelinux.org#${ALPINE_MIRROR%/}#g" /etc/apk/repositories; \
    fi

# 安装必要的运行时依赖
RUN apk add --no-cache ca-certificates tzdata

# 设置时区为中国
ENV TZ=Asia/Shanghai

# 默认按生产配置名读取，实际配置文件通过外部挂载提供
ENV CONFIG_ENV=prod

# 创建非 root 用户运行应用（安全最佳实践）
RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的二进制文件
COPY --from=builder /build/schedule_server .

# 创建配置、日志和上传目录
RUN mkdir -p configs logs uploads && \
    chown -R appuser:appuser configs logs uploads

# 切换到非 root 用户
USER appuser

# 暴露端口（与配置文件中的端口一致）
EXPOSE 26665

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:26665/health || exit 1

# 启动应用
CMD ["./schedule_server"]
