.PHONY: clean build-all gen run

# 設置變量
BINARY_NAME=numscan
GO=go
GOFLAGS=-trimpath
LDFLAGS=-s -w
BUILD_DIR=build

# 支持的平台
PLATFORMS=darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

build-all:
	mkdir -p $(BUILD_DIR)
	@for platform in $(PLATFORMS); do \
		GOOS=$${platform%/*} GOARCH=$${platform#*/} \
		$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-$${platform%/*}-$${platform#*/} .; \
	done

clean:
	rm -rf $(BUILD_DIR)

gen:
	@echo "Generating GORM code..."
	$(GO) run tools/gen/main.go
	@echo "GORM code generation completed!"

run:
	$(GO) run $(GOFLAGS) main.go
