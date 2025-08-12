SHELL := /bin/sh
GO ?= go
APP := arbitr
PKG := ./cmd/arbitrage

# Ensure Go-installed tools are on PATH
export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

.PHONY: all build test lint race run docker k8s-apply k8s-delete tools docker_check kubectl_check

# Helper to ensure a tool exists, otherwise install it via Go
# Usage: $(call ensure_tool,binary,go-install-path@version)
define ensure_tool
	@command -v $(1) >/dev/null 2>&1 || { \
		echo "Installing $(1) ..."; \
		$(GO) install $(2) || exit 1; \
	}
	@command -v $(1) >/dev/null 2>&1 || { \
		echo "ERROR: $(1) not found on PATH even after install. Ensure $$GOBIN or $$GOPATH/bin is in PATH."; \
		exit 1; \
	}
endef

# Tool versions
GOLANGCI_LINT := github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0

all: lint test build

build:
	$(GO) build $(PKG)

run:
	$(GO) run $(PKG)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

# Ensure required dev tools are available
tools:
	$(call ensure_tool,golangci-lint,$(GOLANGCI_LINT))

lint: tools
	$(GO) vet ./...
	golangci-lint run --timeout=5m || true

# Checks for external CLIs that cannot be installed via Go toolchain
docker_check:
	@command -v docker >/dev/null 2>&1 || { echo "ERROR: docker not found. Please install Docker Desktop or Docker Engine and ensure it is on PATH."; exit 1; }

kubectl_check:
	@command -v kubectl >/dev/null 2>&1 || { echo "ERROR: kubectl not found. Please install kubectl and ensure it is on PATH."; exit 1; }

docker: docker_check
	docker build -t $(APP):local .

k8s-apply: kubectl_check
	kubectl apply -f deploy/k8s

k8s-delete: kubectl_check
	kubectl delete -f deploy/k8s || true
