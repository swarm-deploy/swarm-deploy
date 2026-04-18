run:
	docker compose -f docker-compose.local.yaml up

.PHONY: lint
lint:
	golangci-lint run

.PHONY: gen
gen:
	ogen --target ./internal/entrypoints/webserver/generated --clean ./api/api-server.yaml

.PHONY: test
test:
	go test ./...

.PHONY: check
check: lint test
