run:
	docker compose -f docker-compose.local.yaml up

.PHONY: lint
lint:
	golangci-lint run --fix

.PHONY: gen
gen:
	ogen --target ./internal/entrypoints/webserver/generated --clean ./api/api-server.yaml
