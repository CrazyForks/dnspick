APP_NAME := dnspick
BUILD_DIR := builds
LDFLAGS   := -s -w

# 交叉编译目标平台：GOOS/GOARCH
PLATFORMS := \
	linux/amd64 linux/arm64 \
	windows/amd64 windows/arm64 \
	darwin/amd64 darwin/arm64

.PHONY: all build run test vet fmt tidy build-all clean

## 默认：格式化、静态检查、测试、本地构建
all: fmt vet test build

## 构建本机平台二进制
build:
	go build -ldflags="$(LDFLAGS)" -o $(APP_NAME) .

## 运行
run:
	go run .

## 运行测试
test:
	go test ./...

## go vet 静态检查
vet:
	go vet ./...

## 格式化
fmt:
	gofmt -w .

## 整理依赖
tidy:
	go mod tidy

## 交叉编译所有平台到 $(BUILD_DIR)/
build-all: clean
	@mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out="$(BUILD_DIR)/$(APP_NAME)-$$os-$$arch"; \
		if [ "$$os" = "windows" ]; then out="$$out.exe"; fi; \
		echo "--> building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags="$(LDFLAGS)" -o "$$out" . || exit 1; \
	done
	@echo "All builds completed in ./$(BUILD_DIR)"

## 清理构建产物
clean:
	rm -rf $(BUILD_DIR) $(APP_NAME)
