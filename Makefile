SHELL := bash
PKG   := ./cmd/docker-state-exporter
BIN   := docker-state-exporter
IMAGE ?= ghcr.io/dblencowe/docker-state-exporter
VERSION ?= dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -w -s -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

.PHONY: test
test:
	go test ./...

.PHONY: test-race
test-race:
	CGO_ENABLED=1 go test -race -coverprofile=coverage.out ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: lint
lint:
	golangci-lint run

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: docker
docker:
	docker build \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg DATE=$(DATE) \
	  -t $(IMAGE):$(VERSION) .

.PHONY: docker-multi
docker-multi:
	docker buildx build \
	  --platform linux/amd64,linux/arm64,linux/arm/v7 \
	  --build-arg VERSION=$(VERSION) \
	  --build-arg COMMIT=$(COMMIT) \
	  --build-arg DATE=$(DATE) \
	  -t $(IMAGE):$(VERSION) .

.PHONY: clean
clean:
	rm -f $(BIN) coverage.out
