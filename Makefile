.PHONY: deps build run test clean fmt vet docker-build docker-run docker-stop docker-logs docker-clean dev test-shutdown all

# Go binary path (adjust if needed)
GO := /opt/homebrew/bin/go

# Docker settings
IMAGE_NAME := arbitr
CONTAINER_NAME := arbitr
LOG_LEVEL ?= INFO

# === Local Development ===
deps:
	$(GO) mod tidy
	$(GO) mod download

build:
	$(GO) build -o bin/arbitrage ./cmd/arbitrage

run: build
	./bin/arbitrage

test:
	$(GO) test -race -count=1 ./...

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf bin/

# === Docker Commands ===
docker-build:
	@echo "Building Docker image..."
	docker build -t $(IMAGE_NAME) .

docker-run: docker-build
	@echo "Starting container with LOG_LEVEL=$(LOG_LEVEL)..."
	@docker stop $(CONTAINER_NAME) 2>/dev/null || true
	@docker rm $(CONTAINER_NAME) 2>/dev/null || true
	docker run -d \
		--name $(CONTAINER_NAME) \
		-e ARBITR_LOG_LEVEL=$(LOG_LEVEL) \
		-p 19091:19091 \
		--restart always \
		$(IMAGE_NAME)
	@echo "Container started. Use 'make docker-logs' to view logs."

docker-stop:
	@echo "Stopping container..."
	@docker stop $(CONTAINER_NAME) 2>/dev/null || true
	@docker rm $(CONTAINER_NAME) 2>/dev/null || true

docker-restart: docker-stop docker-run

docker-logs:
	docker logs -f $(CONTAINER_NAME)

docker-status:
	docker ps -f name=$(CONTAINER_NAME)

docker-clean: docker-stop
	@echo "Cleaning up Docker resources..."
	docker rmi $(IMAGE_NAME) 2>/dev/null || true
	docker system prune -f

# === Development Helpers ===
dev: fmt vet build

# Test graceful shutdown (local)
test-shutdown: build
	@echo "Testing graceful shutdown..."
	@./bin/arbitrage & \
	PID=$$!; \
	echo "Started with PID: $$PID"; \
	sleep 3; \
	echo "Sending SIGTERM..."; \
	kill -TERM $$PID 2>/dev/null || true; \
	wait $$PID 2>/dev/null || true; \
	echo "Checking for remaining processes..."; \
	REMAINING=$$(ps aux | grep arbitrage | grep -v grep | grep -v make || true); \
	if [ -z "$$REMAINING" ]; then \
		echo "✅ SUCCESS: Graceful shutdown working"; \
	else \
		echo "❌ FAILURE: Found remaining processes"; \
	fi

# Test Docker graceful shutdown
test-docker-shutdown: docker-run
	@echo "Testing Docker graceful shutdown..."
	sleep 5
	@echo "Stopping container..."
	make docker-stop
	@echo "✅ Docker graceful shutdown test completed"

all: deps fmt vet test build

# === Help ===
help:
	@echo "Available commands:"
	@echo "  Local development:"
	@echo "    make build          - Build binary"
	@echo "    make run            - Run locally"
	@echo "    make test           - Run tests"
	@echo "    make dev            - Format, vet, and build"
	@echo ""
	@echo "  Docker:"
	@echo "    make docker-run     - Build and run in Docker (LOG_LEVEL=INFO)"
	@echo "    make docker-run LOG_LEVEL=DEBUG - Run with debug logging"
	@echo "    make docker-stop    - Stop Docker container"
	@echo "    make docker-logs    - View container logs"
	@echo "    make docker-status  - Show container status"
	@echo "    make docker-clean   - Stop and remove all Docker resources"
	@echo ""
	@echo "  Testing:"
	@echo "    make test-shutdown  - Test graceful shutdown (local)"
	@echo "    make test-docker-shutdown - Test graceful shutdown (Docker)"
