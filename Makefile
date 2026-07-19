.PHONY: help fmt test test-postgres build web-deps web-install web-dev web-build web-embed web-test web-typecheck clean dev \
	compose-up compose-db compose-execution-api compose-worker compose-webhook-dispatcher compose-dev compose-dev-worker compose-dev-build compose-dev-logs compose-build compose-down compose-reset compose-logs compose-ps postgres-dsn \
	dev-standalone dev-standalone-postgres dev-api dev-worker worker-once dev-webhook-dispatcher webhook-once \
	webhook-receiver \
	windforce-variable-set windforce-git-token windforce-register windforce-sync windforce-deploy windforce-sample \
	windforce-schema windforce-openapi windforce-control-openapi \
	windforce-run windforce-run-wait windforce-jobs windforce-job windforce-job-result windforce-job-logs windforce-job-cancel \
	ui-guide ui-guide-verify

APP := windforce-core
CMD := ./cmd/windforce-core

LOCAL_GO_WIN := .tmp/tools/go/bin/go.exe
LOCAL_GO_UNIX := .tmp/tools/go/bin/go
ifneq ($(wildcard $(LOCAL_GO_WIN)),)
GO ?= $(LOCAL_GO_WIN)
export PATH := $(abspath .tmp/tools/go/bin):$(PATH)
else ifneq ($(wildcard $(LOCAL_GO_UNIX)),)
GO ?= $(LOCAL_GO_UNIX)
export PATH := $(abspath .tmp/tools/go/bin):$(PATH)
else
GO ?= go
endif

COMPOSE ?= docker compose
COMPOSE_DEV := $(COMPOSE) -f docker-compose.yml -f docker-compose.dev.yml
BUN ?= bun

ifneq (,$(wildcard .env))
include .env
endif

WFL_TMP ?= .tmp
DEV_DIR ?= $(WFL_TMP)/dev
BIN_DIR ?= $(WFL_TMP)/bin
BIN ?= $(BIN_DIR)/$(APP)
STORE ?= $(DEV_DIR)/store
CATALOG ?= $(DEV_DIR)/catalog.json
STATE ?= $(DEV_DIR)/state.json
CACHE ?= $(DEV_DIR)/cache
INPUT ?= $(DEV_DIR)/input.json
OUTPUT ?= $(DEV_DIR)/output.json
WINDFORCE_LITE_API_PORT ?= 18091
WINDFORCE_LITE_WEB_PORT ?= 18090
ADDR ?= 127.0.0.1:$(WINDFORCE_LITE_API_PORT)
WINDFORCE_WEBHOOK_RECEIVER_ADDR ?= 127.0.0.1:19090

WF_API_URL ?= http://127.0.0.1:$(WINDFORCE_LITE_API_PORT)
WF_WORKSPACE ?= default
WF_APP ?= echo
WF_ACTION ?= echo
WF_INPUT_JSON ?= {}
WF_TIMEOUT_MS ?= 5000
WF_JOB_ID ?=
WF_JOB_STATUS ?=
WF_TAIL_BYTES ?=
WF_GIT_SOURCE_NAME ?= $(WF_APP)
WF_GIT_SOURCE_ID ?= 1
WF_ACTOR ?= local-dev
WF_REPO_URL ?= https://github.com/imprun/windforce-core.git
WF_BRANCH ?= main
WF_SUBPATH ?= examples/echo
WF_GIT_CREDS_REF ?=
WF_GIT_AUTH_METHOD ?=
WF_GIT_USERNAME ?=
WF_GIT_PASSWORD_ENV ?=
WF_GIT_TOKEN_ENV ?= WINDFORCE_LITE_GIT_TOKEN
WF_VARIABLE_PATH ?= git/$(WF_GIT_SOURCE_NAME)/credential
WF_VARIABLE_VALUE_ENV ?= $(WF_GIT_TOKEN_ENV)
WF_VARIABLE_APP ?=
WF_VARIABLE_DESCRIPTION ?=

WINDFORCE_POSTGRES_DB ?= windforce_core
WINDFORCE_POSTGRES_USER ?= postgres
WINDFORCE_POSTGRES_PORT ?= 5432
ifneq ($(strip $(WINDFORCE_LITE_DATABASE_URL)),)
POSTGRES_DSN ?= $(WINDFORCE_LITE_DATABASE_URL)
else
POSTGRES_DSN ?= postgres://$(WINDFORCE_POSTGRES_USER)@127.0.0.1:$(WINDFORCE_POSTGRES_PORT)/$(WINDFORCE_POSTGRES_DB)?sslmode=disable
endif
export WINDFORCE_POSTGRES_DB
export WINDFORCE_POSTGRES_USER
export WINDFORCE_POSTGRES_PORT
export WINDFORCE_LITE_API_PORT
export WINDFORCE_LITE_WEB_PORT
export WINDFORCE_LITE_DATABASE_URL
export POSTGRES_DSN

help:
	@echo "targets:"
	@echo "  fmt                    run gofmt"
	@echo "  web-deps               install Web UI dependencies for this worktree"
	@echo "  web-install            install Web UI dependencies"
	@echo "  dev                    start PostgreSQL and Docker air control-plane/worker, then run local Web UI dev server"
	@echo "  web-dev                run the local Vite Web UI dev server with live reload on WINDFORCE_LITE_WEB_PORT"
	@echo "  web-build              build the Web UI to web/dist without touching Go embed assets"
	@echo "  web-embed              build the Web UI and refresh the Go embed assets"
	@echo "  web-test               run Web UI unit tests"
	@echo "  web-typecheck          type-check the Web UI"
	@echo "  test                   run go test ./..."
	@echo "  test-postgres          run PostgreSQL integration test against docker compose"
	@echo "  build                  build $(BIN)"
	@echo "  dev-standalone         run local JSON-state standalone server"
	@echo "  dev-standalone-postgres run PostgreSQL-backed standalone server"
	@echo "  dev-api                run API process with PostgreSQL state"
	@echo "  dev-worker             run worker process with PostgreSQL state"
	@echo "  worker-once            claim at most one PostgreSQL-backed queued job"
	@echo "  dev-webhook-dispatcher run release webhook dispatcher with PostgreSQL state"
	@echo "  webhook-once           process at most one pending webhook delivery"
	@echo "  webhook-receiver       run the signed local contract receiver on WINDFORCE_WEBHOOK_RECEIVER_ADDR"
	@echo "  windforce-variable-set set secret WF_VARIABLE_PATH from WF_VARIABLE_VALUE_ENV through the control API"
	@echo "  windforce-git-token    store WF_GIT_TOKEN_ENV at WF_VARIABLE_PATH for git source auth"
	@echo "  windforce-register     register WF_REPO_URL as WF_GIT_SOURCE_NAME through the control API"
	@echo "  windforce-sync         fetch and validate the latest source for numeric WF_GIT_SOURCE_ID"
	@echo "  windforce-deploy       prepare and publish the latest synchronized source"
	@echo "  windforce-sample       create, sync, and publish WF_APP as a managed sample source"
	@echo "  windforce-schema       print WF_APP/WF_ACTION schemas from the control API"
	@echo "  windforce-openapi      print WF_APP invocation OpenAPI from the control API"
	@echo "  windforce-control-openapi print workspace control-plane OpenAPI"
	@echo "  windforce-run          enqueue WF_APP/WF_ACTION with WF_INPUT_JSON"
	@echo "  windforce-run-wait     run WF_APP/WF_ACTION and wait WF_TIMEOUT_MS"
	@echo "  windforce-jobs         list jobs, optionally filtered by WF_JOB_STATUS"
	@echo "  windforce-job/result/logs/cancel operate on WF_JOB_ID"
	@echo "  compose-up             start control plane, execution API, and Bun Web UI against PostgreSQL"
	@echo "  compose-db             start repo-local PostgreSQL for standalone testing"
	@echo "  compose-execution-api  start the execution API against configured PostgreSQL"
	@echo "  compose-worker         start runtime worker against configured PostgreSQL"
	@echo "  compose-webhook-dispatcher start release webhook dispatcher against configured PostgreSQL"
	@echo "  compose-dev            start PostgreSQL and hot-reload control-plane/execution-api with air"
	@echo "  compose-dev-worker     start PostgreSQL and hot-reload Go runtime worker with docker compose + air"
	@echo "  compose-dev-build      build the dev image that contains Go, Python, git, and air"
	@echo "  compose-dev-logs       follow hot-reload control-plane/execution-api/worker logs"
	@echo "  compose-build          build the Go Docker image; Dockerfile builds and embeds Web UI assets"
	@echo "  compose-down/reset/logs/ps"
	@echo "  ui-guide               regenerate Web UI guide screenshots and markdown (needs bun, go, and a Chromium browser)"
	@echo "  ui-guide-verify        run guide scenarios and verify generated docs"

fmt:
	$(GO) fmt ./...

web-deps web-install:
	cd web && $(BUN) install

web-dev:
	cd web && WINDFORCE_LITE_API_PROXY_TARGET="$(WF_API_URL)" $(BUN) run dev -- --port "$(WINDFORCE_LITE_WEB_PORT)"

dev: compose-dev compose-dev-worker web-dev

web-build:
	cd web && $(BUN) run build

web-embed: web-build
	rm -rf internal/webui/assets
	cp -r web/dist internal/webui/assets

web-test:
	cd web && $(BUN) run test

web-typecheck:
	cd web && $(BUN) run typecheck

test:
	$(GO) test ./...

test-postgres: compose-db
	WINDFORCE_LITE_POSTGRES_TEST_DSN="$(POSTGRES_DSN)" $(GO) test ./internal/state -run Postgres -count=1 -v

build:
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -o "$(BIN)" $(CMD)

compose-up:
	$(COMPOSE) --profile backend up -d control-plane execution-api webhook-dispatcher web

compose-db:
	$(COMPOSE) --profile pg up -d postgres

compose-execution-api:
	$(COMPOSE) --profile backend up -d execution-api

compose-worker:
	$(COMPOSE) --profile worker up -d worker

compose-webhook-dispatcher:
	$(COMPOSE) --profile backend up -d webhook-dispatcher

compose-dev:
	$(COMPOSE_DEV) --profile backend up -d control-plane execution-api webhook-dispatcher

compose-dev-worker:
	$(COMPOSE_DEV) --profile worker up -d worker

compose-dev-build:
	$(COMPOSE_DEV) build control-plane execution-api worker webhook-dispatcher

compose-dev-logs:
	$(COMPOSE_DEV) logs -f control-plane execution-api worker webhook-dispatcher

compose-build:
	$(COMPOSE) build control-plane execution-api worker webhook-dispatcher

compose-down:
	$(COMPOSE) down

compose-reset:
	$(COMPOSE) down -v

compose-logs:
	$(COMPOSE) logs -f postgres control-plane execution-api webhook-dispatcher web worker

compose-ps:
	$(COMPOSE) ps

postgres-dsn:
	@echo "$(POSTGRES_DSN)"

ui-guide:
	node --check tools/ui-guide/capture.mjs
	node tools/ui-guide/capture.mjs

ui-guide-verify:
	node --check tools/ui-guide/capture.mjs
	node tools/ui-guide/capture.mjs --verify

dev-standalone:
	$(GO) run $(CMD) standalone --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --state "$(STATE)" --cache "$(CACHE)"

dev-standalone-postgres: compose-db
	$(GO) run $(CMD) standalone --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

dev-api: compose-up
	$(GO) run $(CMD) api --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

dev-worker: compose-up
	$(GO) run $(CMD) worker --store "$(STORE)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

worker-once: compose-up
	$(GO) run $(CMD) worker --store "$(STORE)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate --once

dev-webhook-dispatcher: compose-db
	$(GO) run $(CMD) webhook-dispatcher --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

webhook-once: compose-db
	$(GO) run $(CMD) webhook-dispatcher --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate --once

webhook-receiver:
	$(GO) run ./examples/webhook-receiver --addr "$(WINDFORCE_WEBHOOK_RECEIVER_ADDR)"

windforce-variable-set:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty variable-set --path "$(WF_VARIABLE_PATH)" --value-env "$(WF_VARIABLE_VALUE_ENV)" --app "$(WF_VARIABLE_APP)" --secret --description "$(WF_VARIABLE_DESCRIPTION)"

windforce-git-token:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty variable-set --path "$(WF_VARIABLE_PATH)" --value-env "$(WF_GIT_TOKEN_ENV)" --secret --description "git access token"

windforce-register:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty register --name "$(WF_GIT_SOURCE_NAME)" --repo-url "$(WF_REPO_URL)" --branch "$(WF_BRANCH)" --subpath "$(WF_SUBPATH)" --creds-ref "$(WF_GIT_CREDS_REF)" --git-auth-method "$(WF_GIT_AUTH_METHOD)" --git-access-token-env "$(WF_GIT_TOKEN_ENV)" --git-username "$(WF_GIT_USERNAME)" --git-password-env "$(WF_GIT_PASSWORD_ENV)"

windforce-sync:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty sync --git-source-id "$(WF_GIT_SOURCE_ID)"

windforce-deploy:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --actor "$(WF_ACTOR)" --pretty deploy --git-source-id "$(WF_GIT_SOURCE_ID)"

windforce-sample:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty sample --app-key "$(WF_APP)"

windforce-schema:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty schema --app "$(WF_APP)" --action "$(WF_ACTION)"

windforce-openapi:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty openapi --app "$(WF_APP)"

windforce-control-openapi:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty control-openapi

windforce-run:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty run --app "$(WF_APP)" --action "$(WF_ACTION)" --input "$(WF_INPUT_JSON)"

windforce-run-wait:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty run-wait --app "$(WF_APP)" --action "$(WF_ACTION)" --timeout-ms "$(WF_TIMEOUT_MS)" --input "$(WF_INPUT_JSON)"

windforce-jobs:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty jobs --status "$(WF_JOB_STATUS)"

windforce-job:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty job --job-id "$(WF_JOB_ID)"

windforce-job-result:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty job-result --job-id "$(WF_JOB_ID)"

windforce-job-logs:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty job-logs --job-id "$(WF_JOB_ID)" $(if $(WF_TAIL_BYTES),--tail-bytes "$(WF_TAIL_BYTES)",)

windforce-job-cancel:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty job-cancel --job-id "$(WF_JOB_ID)"

clean:
	rm -rf "$(WFL_TMP)"
