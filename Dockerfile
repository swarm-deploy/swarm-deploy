# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/swarm-deploy ./cmd/swarm-deploy

FROM alpine:3.21

RUN apk add --no-cache ca-certificates docker-cli tzdata

WORKDIR /etc/swarm-deploy

COPY --from=builder /out/swarm-deploy /usr/local/bin/swarm-deploy

ENTRYPOINT ["/usr/local/bin/swarm-deploy"]
CMD ["-config", "/etc/swarm-deploy/swarm-deploy.yaml"]

