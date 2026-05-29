# syntax=docker/dockerfile:1

FROM node:22-alpine AS ui-builder

WORKDIR /ui

COPY ui/package.json ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm install

COPY ui/index.html ui/styles.css ui/vite.config.ts ui/tsconfig.json ./
COPY ui/src ./src
RUN npm run build

FROM golang:1.26.2-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
COPY --from=ui-builder /ui/dist ./ui/dist

ARG TARGETOS=linux
ARG TARGETARCH=amd64

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/swarm-deploy ./cmd/swarm-deploy

FROM alpine:3.21.7

RUN apk add --no-cache ca-certificates docker-cli tzdata

WORKDIR /etc/swarm-deploy

COPY --from=builder /out/swarm-deploy /usr/local/bin/swarm-deploy

LABEL org.opencontainers.image.title="swarm-deploy"
LABEL org.opencontainers.image.description="GitOps controller for Docker Swarm"
LABEL org.opencontainers.image.url="https://github.com/swarm-deploy/swarm-deploy"
LABEL org.opencontainers.image.source="https://github.com/swarm-deploy/swarm-deploy"
LABEL org.opencontainers.image.vendor="swarm-deploy"
LABEL org.opencontainers.image.version="$APP_VERSION"
LABEL org.opencontainers.image.created="$BUILD_TIME"
LABEL org.opencontainers.image.licenses="Apache 2.0"
LABEL org.swarm-deploy.sd=true
LABEL org.swarm-deploy.service.type="DeploymentManagementSystem"

ENTRYPOINT ["/usr/local/bin/swarm-deploy"]
CMD ["-config", "/etc/swarm-deploy/swarm-deploy.yaml"]

