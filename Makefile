SHELL := /bin/sh
GO ?= go
APP := arbitr
PKG := ./cmd/arbitrage

.PHONY: all build test lint race run docker k8s-apply k8s-delete

all: lint test build

build:
	$(GO) build $(PKG)

run:
	$(GO) run $(PKG)

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...
	golangci-lint run --timeout=5m || true

docker:
	docker build -t $(APP):local .

k8s-apply:
	kubectl apply -f deploy/k8s

k8s-delete:
	kubectl delete -f deploy/k8s || true
