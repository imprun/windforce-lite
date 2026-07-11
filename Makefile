.PHONY: help fmt test test-postgres build web-install web-build web-typecheck clean \
	compose-up compose-db compose-worker compose-build compose-down compose-reset compose-logs compose-ps postgres-dsn \
	dev-standalone dev-standalone-postgres dev-api dev-worker worker-once \
	windforce-variable-set windforce-git-token windforce-register windforce-sync windforce-deploy windforce-sample \
	windforce-schema windforce-openapi windforce-control-openapi \
	windforce-run windforce-run-wait windforce-jobs windforce-job windforce-job-result windforce-job-logs windforce-job-cancel \
	ui-guide ui-guide-verify

APP := windforce-lite
CMD := ./cmd/windforce-lite

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
ADDR ?= 127.0.0.1:8080

WINDFORCE_LITE_API_PORT ?= 18090
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
WF_REPO_URL ?= https://github.com/imprun/windforce-lite.git
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

WINDFORCE_POSTGRES_DB ?= windforce_lite
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
export WINDFORCE_LITE_DATABASE_URL
export POSTGRES_DSN

help:
	@echo "targets:"
	@echo "  fmt                    run gofmt"
	@echo "  web-install            install Next.js Web UI dependencies"
	@echo "  web-build              build Next.js Web UI and sync Go embed assets"
	@echo "  web-typecheck          type-check the Next.js Web UI"
	@echo "  test                   build Web UI and run go test ./..."
	@echo "  test-postgres          run PostgreSQL integration test against docker compose"
	@echo "  build                  build $(BIN)"
	@echo "  dev-standalone         run local JSON-state standalone server"
	@echo "  dev-standalone-postgres run PostgreSQL-backed standalone server"
	@echo "  dev-api                run API process with PostgreSQL state"
	@echo "  dev-worker             run worker process with PostgreSQL state"
	@echo "  worker-once            claim at most one PostgreSQL-backed queued job"
	@echo "  windforce-variable-set set secret WF_VARIABLE_PATH from WF_VARIABLE_VALUE_ENV through the control API"
	@echo "  windforce-git-token    store WF_GIT_TOKEN_ENV at WF_VARIABLE_PATH for git source auth"
	@echo "  windforce-register     register WF_REPO_URL as WF_GIT_SOURCE_NAME through the control API"
	@echo "  windforce-sync         sync numeric WF_GIT_SOURCE_ID through the control API"
	@echo "  windforce-deploy       deploy numeric WF_GIT_SOURCE_ID through the control API"
	@echo "  windforce-sample       create and sync WF_APP as a managed sample source"
	@echo "  windforce-schema       print WF_APP/WF_ACTION schemas from the control API"
	@echo "  windforce-openapi      print WF_APP invocation OpenAPI from the control API"
	@echo "  windforce-control-openapi print workspace control-plane OpenAPI"
	@echo "  windforce-run          enqueue WF_APP/WF_ACTION with WF_INPUT_JSON"
	@echo "  windforce-run-wait     run WF_APP/WF_ACTION and wait WF_TIMEOUT_MS"
	@echo "  windforce-jobs         list jobs, optionally filtered by WF_JOB_STATUS"
	@echo "  windforce-job/result/logs/cancel operate on WF_JOB_ID"
	@echo "  compose-up             start Postgres and control-plane API"
	@echo "  compose-db             start only Postgres"
	@echo "  compose-worker         start Postgres and runtime worker"
	@echo "  compose-build          build the control-plane API image"
	@echo "  compose-down/reset/logs/ps"
	@echo "  ui-guide               regenerate Web UI guide screenshots and markdown"
	@echo "  ui-guide-verify        run guide scenarios and verify generated docs"

fmt:
	$(GO) fmt ./...

web-install:
	cd web && $(BUN) install

web-build:
	cd web && $(BUN) run build

web-typecheck:
	cd web && $(BUN) run typecheck

test: web-build
	$(GO) test ./...

test-postgres: compose-db
	WINDFORCE_LITE_POSTGRES_TEST_DSN="$(POSTGRES_DSN)" $(GO) test ./internal/state -run Postgres -count=1 -v

build: web-build
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -o "$(BIN)" $(CMD)

compose-up:
	$(COMPOSE) up -d postgres control-plane

compose-db:
	$(COMPOSE) up -d postgres

compose-worker:
	$(COMPOSE) up -d postgres worker

compose-build:
	$(COMPOSE) build control-plane

compose-down:
	$(COMPOSE) down

compose-reset:
	$(COMPOSE) down -v

compose-logs:
	$(COMPOSE) logs -f postgres control-plane worker

compose-ps:
	$(COMPOSE) ps

postgres-dsn:
	@echo "$(POSTGRES_DSN)"

ui-guide: web-build compose-build
	node --check tools/ui-guide/capture.mjs
	node tools/ui-guide/capture.mjs

ui-guide-verify: web-build compose-build
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

windforce-variable-set:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty variable-set --path "$(WF_VARIABLE_PATH)" --value-env "$(WF_VARIABLE_VALUE_ENV)" --app "$(WF_VARIABLE_APP)" --secret --description "$(WF_VARIABLE_DESCRIPTION)"

windforce-git-token:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty variable-set --path "$(WF_GIT_CREDS_REF)" --value-env "$(WF_GIT_TOKEN_ENV)" --secret --description "git access token"

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
