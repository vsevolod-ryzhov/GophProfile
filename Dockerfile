# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.1

FROM golang:${GO_VERSION}-alpine AS builder
ARG CMD=server
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w" \
        -o /out/app ./cmd/${CMD}

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=builder /out/app /app/app
COPY migrations /app/migrations
USER nonroot:nonroot
ENTRYPOINT ["/app/app"]
