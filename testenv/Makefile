DOCKER := docker --host unix:///var/run/docker.sock
COMPOSE := $(DOCKER) compose --project-name swarm-deploy-testenv
GITOPS_GIT := git --git-dir=.gitops.git --work-tree=gitops

.DEFAULT_GOAL := up

.PHONY: build up down seed-git check-swarm check-token
build:
	$(COMPOSE) build

up: check-swarm check-token seed-git
	$(COMPOSE) up --build --detach

down:
	$(COMPOSE) down

seed-git:
	@if [ ! -f .gitops.git/HEAD ]; then git init --bare --initial-branch=main .gitops.git; fi
	$(GITOPS_GIT) add --all
	@if $(GITOPS_GIT) diff --cached --quiet; then exit 0; fi; \
		$(GITOPS_GIT) -c user.name="swarm-deploy testenv" -c user.email=testenv@swarm-deploy.local \
			-c commit.gpgsign=false commit -m "Update test stacks"

check-swarm:
	@test "$$($(DOCKER) info --format '{{.Swarm.ControlAvailable}}')" = true || \
		{ echo "Use a local Docker Swarm manager on /var/run/docker.sock. See testenv/README.md." >&2; exit 1; }

check-token:
	@test -s .openai-api-token || \
		{ echo "Put your API token in testenv/.openai-api-token." >&2; exit 1; }
