.PHONY: help build run test ci-test ci-build ci-lint agent-config-check clean docker-build docker-build-prod docker-run docker-stop deploy

# 默认目标
help:
	@echo "Schedule Server - 可用命令:"
	@echo "  make build        - 编译应用"
	@echo "  make run          - 本地运行（开发模式）"
	@echo "  make test         - 运行测试"
	@echo "  make ci-test      - 运行 CI 测试"
	@echo "  make ci-build     - 运行 CI 构建"
	@echo "  make ci-lint      - 运行 CI lint"
	@echo "  make agent-config-check - 校验 Agent 运行时配置并输出安全指纹"
	@echo "  make clean        - 清理构建产物"
	@echo "  make docker-build - 构建 Docker 镜像"
	@echo "  make docker-build-prod - 以 CI 标签到构建 Docker 镜像"
	@echo "  make docker-run   - 运行 Docker 容器"
	@echo "  make docker-stop  - 停止 Docker 容器"
	@echo "  make deploy       - 完整部署（构建+运行）"

# 编译应用
build:
	@echo "编译应用..."
	go build -o bin/schedule_server ./cmd/main.go
	@echo "编译完成: bin/schedule_server"

# 本地运行
run:
	@echo "启动应用（开发模式）..."
	ENV=dev go run ./cmd/main.go

# 运行测试
test:
	@echo "运行测试..."
	go test -v ./...

ci-test:
	@echo "运行 CI 测试..."
	go test ./...

ci-build:
	@echo "运行 CI 构建..."
	go build -o bin/schedule_server ./cmd/main.go

ci-lint:
	@echo "运行 CI lint..."
	golangci-lint run

agent-config-check:
	go run ./cmd/main.go agent-config-check

# 清理构建产物
clean:
	@echo "清理构建产物..."
	rm -rf bin/
	rm -rf logs/*.log
	@echo "清理完成"

# 构建 Docker 镜像
docker-build:
	@echo "构建 Docker 镜像..."
	docker build -t schedule-server:latest .

docker-build-prod:
	@echo "以 CI 标签构建 Docker 镜像..."
	docker build -t schedule-server:ci .

# 运行 Docker 容器
docker-run:
	@echo "启动 Docker 容器..."
	docker compose up -d

# 停止 Docker 容器
docker-stop:
	@echo "停止 Docker 容器..."
	docker compose down

# 完整部署
deploy:
	@echo "开始部署..."
	./deploy.sh deploy

# 查看日志
logs:
	docker logs -f schedule-server

# 进入容器
shell:
	docker exec -it schedule-server sh
