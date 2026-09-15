run:
	docker compose -f docker-compose.local.yaml up

.PHONY: lint
lint:
	golangci-lint run

.PHONY: gen
gen:
	go run -mod=mod github.com/ogen-go/ogen/cmd/ogen@v1.20.1 --target ./internal/entrypoints/webserver/generated --clean ./api/api-server.yaml
	go generate ./...

.PHONY: test
test:
	go test ./...

.PHONY: check
check: lint test

.PHONY: test-cli
cli:
	go run ./cmd/sd/main.go lint ./example/04-assistant/swarm-deploy.yaml

