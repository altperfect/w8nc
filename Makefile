SHELL := /bin/sh

GO ?= go
NPM ?= npm
COMPOSE ?= docker compose
APP_SERVICE ?= app
APP_URL ?= http://127.0.0.1:8080
WEB_DIR ?= web
STATIC_DIR ?= internal/static/dist
GOCACHE ?= /tmp/w8nc-gocache
GOMODCACHE ?= /tmp/w8nc-gomodcache

.DEFAULT_GOAL := help

.PHONY: help init-secrets require-secrets rotate-encryption-key deps test test-web test-go build build-web sync-static build-go ci docker-build prepare-app-data up deploy redeploy restart set-password reset-login-attempts ps logs health down clean

help:
	@printf "Targets:\n"
	@printf "  make deps          Install frontend dependencies\n"
	@printf "  make init-secrets  Create local .env with generated secrets\n"
	@printf "  make test          Run frontend and Go tests\n"
	@printf "  make build         Build frontend, embed assets, and build Go server\n"
	@printf "  make ci            Run test and build\n"
	@printf "  make deploy        Build Docker app image and start compose stack\n"
	@printf "  make redeploy      Run tests, rebuild image, and restart compose stack\n"
	@printf "  make prepare-app-data  Ensure app data volume is writable by the rootless app user\n"
	@printf "  make rotate-encryption-key  Re-encrypt stored secrets under a new key\n"
	@printf "  make set-password  Generate and set a new login password\n"
	@printf "  make reset-login-attempts  Clear login rate-limit lockouts\n"
	@printf "  make health        Check the running app health endpoint\n"
	@printf "  make ps            Show compose service status\n"
	@printf "  make logs          Follow app container logs\n"
	@printf "  make down          Stop compose stack\n"
	@printf "  make clean         Remove generated local build artifacts\n"

init-secrets:
	@if [ -f .env ]; then \
		printf ".env already exists; leaving it unchanged.\n"; \
	else \
		command -v openssl >/dev/null 2>&1 || { printf "openssl is required to generate secrets.\n" >&2; exit 1; }; \
		enc_key=$${ENCRYPTION_KEY:-$$(openssl rand -base64 32)}; \
		session_secret=$${SESSION_SECRET:-$$(openssl rand -base64 32)}; \
		umask 077; \
		{ \
			printf "SESSION_SECRET=%s\n" "$$session_secret"; \
			printf "ENCRYPTION_KEY=%s\n" "$$enc_key"; \
		} > .env; \
		printf "Created .env with local secrets.\n"; \
	fi

require-secrets:
	@if [ ! -f .env ] && { [ -z "$$SESSION_SECRET" ] || [ -z "$$ENCRYPTION_KEY" ]; }; then \
		printf "Missing SESSION_SECRET or ENCRYPTION_KEY. Run 'make init-secrets' for a new install, or create .env with the current ENCRYPTION_KEY before deploying an existing database.\n" >&2; \
		exit 1; \
	fi

deps:
	cd $(WEB_DIR) && $(NPM) install

test:
	$(MAKE) test-web
	$(MAKE) test-go

test-web:
	cd $(WEB_DIR) && $(NPM) test

test-go:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test ./...

build:
	$(MAKE) build-web
	$(MAKE) sync-static
	$(MAKE) build-go

build-web:
	cd $(WEB_DIR) && $(NPM) run build

sync-static:
	rm -rf $(STATIC_DIR)
	cp -R $(WEB_DIR)/dist $(STATIC_DIR)

build-go:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) build ./cmd/server

ci:
	$(MAKE) test
	$(MAKE) build

docker-build:
	$(COMPOSE) build $(APP_SERVICE)

prepare-app-data:
	$(COMPOSE) run --rm --no-deps --user root $(APP_SERVICE) chown -R 10001:10001 /app/data

up:
	$(COMPOSE) up -d

deploy: require-secrets
	$(MAKE) docker-build
	$(MAKE) prepare-app-data
	$(MAKE) up
	$(MAKE) health

redeploy:
	$(MAKE) test
	$(MAKE) deploy

restart:
	$(COMPOSE) restart $(APP_SERVICE)

rotate-encryption-key: require-secrets
	@command -v openssl >/dev/null 2>&1 || { printf "openssl is required to generate a new encryption key.\n" >&2; exit 1; }
	@new_key=$$(openssl rand -base64 32); \
	NEW_ENCRYPTION_KEY="$$new_key" $(COMPOSE) exec -T -e NEW_ENCRYPTION_KEY $(APP_SERVICE) /app/w8nc rotate-encryption-key && \
	tmp=$$(mktemp .env.XXXXXX) && \
	awk -v key="$$new_key" 'BEGIN { done=0 } /^ENCRYPTION_KEY=/ { print "ENCRYPTION_KEY=" key; done=1; next } { print } END { if (!done) print "ENCRYPTION_KEY=" key }' .env > "$$tmp" && \
	chmod 600 "$$tmp" && \
	mv "$$tmp" .env && \
	$(COMPOSE) up -d --force-recreate $(APP_SERVICE) >/dev/null && \
	printf "Encryption key rotated, .env updated, and app restarted.\n"

set-password:
	@$(COMPOSE) exec -T $(APP_SERVICE) /app/w8nc set-password
	@$(COMPOSE) kill -s USR1 $(APP_SERVICE) >/dev/null
	@printf "Login attempt lockouts cleared.\n"

reset-login-attempts:
	$(COMPOSE) kill -s USR1 $(APP_SERVICE)

ps:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs -f $(APP_SERVICE)

health:
	@for attempt in $$(seq 1 12); do \
		if curl -fsS $(APP_URL)/api/health; then \
			printf "\n"; \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	printf "Health check failed after 12 attempts.\n" >&2; \
	exit 1
	@printf "\n"

down:
	$(COMPOSE) down

clean:
	rm -rf $(STATIC_DIR) $(WEB_DIR)/dist server server.exe
