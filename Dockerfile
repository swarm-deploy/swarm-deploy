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

ENTRYPOINT ["/usr/local/bin/swarm-deploy"]
CMD ["-config", "/etc/swarm-deploy/swarm-deploy.yaml"]

