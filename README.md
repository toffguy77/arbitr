Arbitrage Trading System (Go) - Skeleton

This repository contains a high-performance, modular skeleton for an arbitrage crypto-trading system. It focuses on architecture, testability, low latency, extendability, and compliance with the constraints of the environment (user in RU, hosting in EU).

Key features included:
- Project structure and interfaces
- Stubs for exchange adapters (no real keys or network calls)
- ComplianceGuard with allow/deny and metrics
- Prometheus metrics registry (including latency/RTT/compliance/time drift)
- SlippageModel scaffold (integral over order book depth)
- Pseudocode for triangular arbitrage
- gRPC control.proto for Start/Stop/Status/RiskLimitsUpdate/ComplianceRulesUpdate
- Adaptive rate limiting scaffold based on RTT degradation
- EgressManager with region abstraction (EU-West) and future multi-region extension

Note: This is a skeleton for development and testing. Real connectivity, secrets, and production concerns are intentionally omitted.

Running locally
- Build: go build ./cmd/arbitrage
- Run: go run ./cmd/arbitrage
- Health: curl -sf http://127.0.0.1:9090/healthz
- Metrics: curl -sf http://127.0.0.1:9090/metrics

Configuration
- By default, sensible defaults are used (EU-West region, info log level, addr :9090).
- You can specify a YAML config path via ARBITR_CONFIG=/path/config.yaml.
- Environment overrides (take precedence over file):
  - ARBITR_REGION
  - ARBITR_LOG_LEVEL
  - ARBITR_HTTP_ADDR (default :9090)
  - ARBITR_PPROF (true/false)
  - ARBITR_ADMIN_ALLOW_CIDRS="10.0.0.0/8,192.168.0.0/16"
  - ARBITR_TRADING_ENABLED (true/false)
  - ARBITR_TRADING_LIVE (true/false)
  - ARBITR_TRADING_PAIRS (CSV)
  - ARBITR_MAX_NOTIONAL_USD, ARBITR_MAX_ORDERS_PER_MIN, ARBITR_ALLOWED_SYMBOLS (CSV)
  - ARBITR_BYBIT_API_KEY, ARBITR_BYBIT_SECRET (API credentials)
  - ARBITR_BYBIT_BASE_URL (override Bybit API base URL; e.g., https://api.bybit.com for mainnet)

See config.example.yaml for a starting point.
Docker
- Build image: docker build -t arbitr:local .
- Run: docker run --rm -p 9090:9090 arbitr:local

GitHub Container Registry (GHCR)
- Образы будут публиковаться в: ghcr.io/toffguy77/arbitr:latest (main), :vX.Y.Z (теги), :sha-<shortsha>
- Не требуется отдельный токен: используется встроенный GITHUB_TOKEN с правами packages:write (указано в workflow permissions)
- Убедитесь, что включён GitHub Packages для аккаунта и настроена видимость пакета при необходимости

Kubernetes (пример)
- Применить манифесты: make k8s-apply
- Удалить манифесты: make k8s-delete

Admin-доступ к /metrics и /debug/pprof
- По умолчанию разрешены только локальные адреса (127.0.0.0/8, ::1/128)
- Настроить: server.admin_allow_cidrs в конфиге или ARBITR_ADMIN_ALLOW_CIDRS="10.0.0.0/8,192.168.0.0/16"

Makefile цели
- build, run, test, race, lint, docker, k8s-apply, k8s-delete
