VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/enthonysclafani/automatizzatore/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: all web build test lint validate-templates check docker clean

all: check web build

web: ## Build the dashboard into internal/webui/dist (needs Node 22)
	cd web && npm ci --no-audit --no-fund && npm run build

build: ## Build bin/pendolo (run `make web` first to include the dashboard)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/pendolo ./cmd/pendolo

test: ## Run tests with the race detector
	go test -race -count=1 ./...

lint: ## Run golangci-lint and shellcheck
	golangci-lint run ./...
	shellcheck templates/*/scripts/*.sh deploy/*.sh

validate-templates: ## Validate the bundled automation templates
	go run ./cmd/pendolo validate --strict templates

check: test lint validate-templates ## Everything CI runs, except the Docker build

docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) -t pendolo:$(VERSION) .

clean:
	rm -rf bin dist
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
