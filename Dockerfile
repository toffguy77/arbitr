# Multi-stage build
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_TIME=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags "-X arbitr/internal/infra/version.Version=$VERSION -X arbitr/internal/infra/version.Commit=$COMMIT -X arbitr/internal/infra/version.BuildTime=$BUILD_TIME" -o /out/arbitr ./cmd/arbitrage

FROM gcr.io/distroless/static:nonroot
ENV ARBITR_HTTP_ADDR=":9090"
COPY --from=builder /out/arbitr /usr/local/bin/arbitr
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/arbitr"]
