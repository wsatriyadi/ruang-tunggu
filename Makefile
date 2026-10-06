# YouTube Live Server — build and release.
#
# The deployment target is a Linux VPS or single-board box, so every target here
# produces a Linux ELF binary. The frontend is built once into web/dist and
# embedded into the served asset directory.

BINARY      := ytliveserver
WEB_DIR     := web
DIST_DIR    := web/dist
VERSION     ?= 1.0.0
LDFLAGS     := -s -w -X main.version=$(VERSION)

# Device matrix. The 2 GB ARM board is a first-class target, not an afterthought.
PLATFORMS := linux/amd64 linux/arm64 linux/arm

.PHONY: help
help: ## Show available targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

# ---------------------------------------------------------------- frontend ---

.PHONY: web
web: ## Build the React frontend into web/dist
	cd $(WEB_DIR) && npm ci
	cd $(WEB_DIR) && npm run build

.PHONY: web-dev
web-dev: ## Start the Vite dev server (proxies the API to :8080)
	cd $(WEB_DIR) && npm run dev

# ------------------------------------------------------------------- build ---

.PHONY: build
build: web ## Build for the host platform
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/ytliveserver

.PHONY: build-linux
build-linux: web ## Build a native Linux binary (amd64 on an amd64 host)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/ytliveserver

# ARM64 with a v7-compatible baseline, the usual choice for SBC images.
.PHONY: build-arm64
build-arm64: web ## Cross-compile for Linux ARM64
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY)-linux-arm64 ./cmd/ytliveserver

# 32-bit ARMv7.
.PHONY: build-arm
build-arm: web ## Cross-compile for Linux ARMv7
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY)-linux-armv7 ./cmd/ytliveserver

.PHONY: release
release: web ## Build every supported Linux target into dist/
	@mkdir -p dist
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		case $$arch in \
			arm) out="$(BINARY)-$$os-armv7"; armflag="GOARM=7" ;; \
			*)   out="$(BINARY)-$$os-$$arch"; armflag="" ;; \
		esac; \
		echo "building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $$armflag \
			go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$$out ./cmd/ytliveserver || exit 1; \
	done
	@ls -lh dist/

# -------------------------------------------------------------------- test ---

.PHONY: check
check: ## Vet and format-check the Go code
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || \
		(echo "gofmt needed:"; gofmt -l cmd internal; exit 1)

.PHONY: test-api
test-api: ## Run the end-to-end API suite (expects BASE to be running)
	python3 scripts/smoke_test.py

# ------------------------------------------------------------------- clean ---

.PHONY: clean
clean: ## Remove build output
	rm -f $(BINARY) $(BINARY)-linux-*
	rm -rf dist

.PHONY: clean-all
clean-all: clean ## Also remove the frontend build and dependencies
	rm -rf $(DIST_DIR) $(WEB_DIR)/node_modules

# --------------------------------------------------------------------- run ---

.PHONY: run
run: build ## Build and start with local defaults
	./$(BINARY) -addr 0.0.0.0:8080 -data ./data

.PHONY: run-dev
run-dev: ## Start the backend against the already-built frontend
	go run ./cmd/ytliveserver -addr 127.0.0.1:8080 -data ./data -frontend ./$(DIST_DIR)