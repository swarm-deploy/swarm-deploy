run:
	docker compose -f docker-compose.local.yaml up

build:
	#docker build . -t artarts36/swarm-deploy:latest
	docker build . -t wmb-prod.cr.cloud.ru/infra/deploy/swarm-deploy:latest
	docker push wmb-prod.cr.cloud.ru/infra/deploy/swarm-deploy:latest

.PHONY: lint
lint:
	golangci-lint run --fix

