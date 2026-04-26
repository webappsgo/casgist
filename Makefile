# CasGists Makefile - BASE SPEC Compliant
# Semantic versioning with auto-increment from ./release.txt

# Project configuration
PROJECTNAME := casgist
PROJECTORG := casapps

# Version management from ./release.txt or fallback
ifeq ($(VERSION),)
  ifneq (,$(wildcard ./release.txt))
    VERSION := $(shell cat ./release.txt)
  else
    VERSION := $(shell git describe --tags --always 2>/dev/null || echo "1.0.0")
    $(shell echo "$(VERSION)" > ./release.txt)
  endif
endif

# Build metadata
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X main.BuildDate=$(BUILD_DATE) -X main.GitCommit=$(GIT_COMMIT)"
BUILD_FLAGS := -trimpath

# Build directories
BINDIR := ./binaries
RELEASEDIR := ./releases

# Platform configurations (amd64 and arm64 for all)
PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64 \
	freebsd/amd64 \
	freebsd/arm64 \
	openbsd/amd64 \
	openbsd/arm64 \
	netbsd/amd64 \
	netbsd/arm64

.PHONY: all build release docker test clean help version

.DEFAULT_GOAL := build

all: clean build test ## Clean, build, and test

build: ## Build for all platforms + host binary
	@echo "🔨 Building $(PROJECTNAME) v$(VERSION) for all platforms..."
	@mkdir -p $(BINDIR)
	@# Build for all platforms
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d'/' -f1); \
		GOARCH=$$(echo $$platform | cut -d'/' -f2); \
		output_name=$(PROJECTNAME)-$$GOOS-$$GOARCH; \
		if [ "$$GOOS" = "windows" ]; then \
			output_name=$$output_name.exe; \
		fi; \
		echo "  ├─ Building $$output_name..."; \
		CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH go build $(BUILD_FLAGS) $(LDFLAGS) \
			-o $(BINDIR)/$$output_name ./src/cmd/casgists || exit 1; \
		if echo "$$output_name" | grep -q "linux-"; then \
			if command -v strip >/dev/null 2>&1; then \
				strip $(BINDIR)/$$output_name 2>/dev/null || true; \
			fi; \
		fi; \
	done
	@# Build host binary
	@echo "  └─ Building host binary $(PROJECTNAME)..."
	@CGO_ENABLED=0 go build $(BUILD_FLAGS) $(LDFLAGS) -o $(BINDIR)/$(PROJECTNAME) ./src/cmd/casgists
	@echo "✅ Build complete: $(BINDIR)/"
	@ls -lh $(BINDIR)/ | tail -5

release: build ## Create GitHub release with auto-increment version
	@echo "🚀 Preparing release v$(VERSION)..."
	@# Auto-increment patch version
	@current_version=$(VERSION); \
	IFS='.' read -r major minor patch <<< "$$current_version"; \
	new_patch=$$((patch + 1)); \
	new_version="$$major.$$minor.$$new_patch"; \
	echo "$$new_version" > ./release.txt; \
	echo "  ├─ Version: $(VERSION) → $$new_version"
	@# Create release directory and package binaries
	@mkdir -p $(RELEASEDIR)
	@echo "  ├─ Packaging binaries..."
	@for file in $(BINDIR)/$(PROJECTNAME)-*; do \
		base=$$(basename $$file); \
		if echo "$$base" | grep -q ".exe"; then \
			cp $$file $(RELEASEDIR)/$$base; \
			cd $(RELEASEDIR) && zip $${base%.exe}.zip $$base && rm $$base && cd ..; \
		else \
			cp $$file $(RELEASEDIR)/$$base; \
			cd $(RELEASEDIR) && tar -czf $$base.tar.gz $$base && rm $$base && cd ..; \
		fi; \
	done
	@# Generate checksums
	@echo "  ├─ Generating checksums..."
	@cd $(RELEASEDIR) && sha256sum * > SHA256SUMS.txt
	@# Create GitHub release
	@echo "  ├─ Creating GitHub release..."
	@new_version=$$(cat ./release.txt); \
	if command -v gh >/dev/null 2>&1; then \
		gh release delete v$$new_version -y 2>/dev/null || true; \
		gh release create v$$new_version $(RELEASEDIR)/* \
			--title "$(PROJECTNAME) v$$new_version" \
			--notes "Release v$$new_version - Built on $(BUILD_DATE)" \
			--repo $(PROJECTORG)/$(PROJECTNAME); \
		echo "✅ GitHub release v$$new_version created"; \
	else \
		echo "⚠️  gh CLI not found - skipping GitHub release"; \
		echo "   Install: https://cli.github.com/"; \
	fi
	@echo "✅ Release complete: v$$(cat ./release.txt)"

docker: ## Build and push multi-arch Docker images to ghcr.io
	@echo "🐳 Building Docker images v$(VERSION)..."
	@# Setup buildx builder
	@docker buildx create --use --name $(PROJECTNAME)-builder 2>/dev/null || true
	@# Build and push multi-arch images
	@echo "  ├─ Building for linux/amd64,linux/arm64..."
	@docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		--build-arg GIT_COMMIT=$(GIT_COMMIT) \
		-t ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION) \
		-t ghcr.io/$(PROJECTORG)/$(PROJECTNAME):latest \
		--push \
		. || (echo "❌ Docker build failed - check if logged into ghcr.io" && exit 1)
	@# Cleanup builder
	@docker buildx rm $(PROJECTNAME)-builder 2>/dev/null || true
	@echo "✅ Docker images pushed to ghcr.io/$(PROJECTORG)/$(PROJECTNAME):$(VERSION)"

test: ## Run all tests
	@echo "🧪 Running tests..."
	@go test -v -race -coverprofile=coverage.txt ./src/...
	@echo "✅ Tests complete"

clean: ## Clean build artifacts
	@echo "🧹 Cleaning..."
	@rm -rf $(BINDIR) $(RELEASEDIR) build dist coverage.txt coverage.html
	@echo "✅ Cleaned"

version: ## Show current version
	@echo "$(VERSION)"

help: ## Show this help message
	@echo "$(PROJECTNAME) Makefile v$(VERSION)"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo ""
	@echo "Version: $(VERSION)"
	@echo "Commit:  $(GIT_COMMIT)"
	@echo "Date:    $(BUILD_DATE)"
