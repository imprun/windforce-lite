.PHONY: help fmt test test-postgres build clean \
	sync-example run-example smoke \
	compose-up compose-down compose-reset compose-logs compose-ps postgres-dsn \
	dev-standalone dev-standalone-postgres dev-trigger dev-api dev-worker worker-once \
	windforce-register windforce-sync windforce-schema windforce-openapi

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
WAIT ?= 30s

WF_API_URL ?= http://127.0.0.1:8080
WF_WORKSPACE ?= default
WF_APP ?= echo
WF_ACTION ?= echo
WF_GIT_SOURCE_ID ?= $(WF_APP)
WF_REPO_URL ?= .
WF_BRANCH ?= main
WF_SUBPATH ?= examples/echo
WF_GIT_TOKEN_ENV ?=

WINDFORCE_POSTGRES_DB ?= windforce_lite
WINDFORCE_POSTGRES_USER ?= postgres
WINDFORCE_POSTGRES_PORT ?= 5432
POSTGRES_DSN ?= postgres://$(WINDFORCE_POSTGRES_USER)@127.0.0.1:$(WINDFORCE_POSTGRES_PORT)/$(WINDFORCE_POSTGRES_DB)?sslmode=disable
export WINDFORCE_POSTGRES_DB
export WINDFORCE_POSTGRES_USER
export WINDFORCE_POSTGRES_PORT

help:
	@echo "targets:"
	@echo "  fmt                    run gofmt"
	@echo "  test                   run go test ./..."
	@echo "  test-postgres          run PostgreSQL integration test against docker compose"
	@echo "  build                  build $(BIN)"
	@echo "  smoke                  sync and run examples/echo through the direct CLI"
	@echo "  dev-standalone         run local JSON-state standalone server"
	@echo "  dev-standalone-postgres run PostgreSQL-backed standalone server"
	@echo "  dev-trigger            run trigger process with PostgreSQL state"
	@echo "  dev-api                run API process with PostgreSQL state"
	@echo "  dev-worker             run worker process with PostgreSQL state"
	@echo "  worker-once            claim at most one PostgreSQL-backed queued job"
	@echo "  windforce-register     register WF_REPO_URL as a git source through the control API"
	@echo "  windforce-sync         sync WF_GIT_SOURCE_ID through the control API"
	@echo "  windforce-schema       print WF_APP/WF_ACTION schemas from the control API"
	@echo "  windforce-openapi      print WF_APP invocation OpenAPI from the control API"
	@echo "  compose-up/down/reset/logs/ps"

fmt:
	$(GO) fmt ./...

test:
	$(GO) test ./...

test-postgres: compose-up
	WINDFORCE_LITE_POSTGRES_TEST_DSN="$(POSTGRES_DSN)" $(GO) test ./internal/state -run Postgres -count=1 -v

build:
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -o "$(BIN)" $(CMD)

sync-example:
	@mkdir -p "$(DEV_DIR)"
	$(GO) run $(CMD) sync --source examples/echo --store "$(STORE)" --catalog "$(CATALOG)"

run-example: sync-example
	@printf '%s\n' '{"message":"hello from make"}' > "$(INPUT)"
	$(GO) run $(CMD) run --app echo --action echo --input "$(INPUT)" --output "$(OUTPUT)" --store "$(STORE)" --catalog "$(CATALOG)" --cache "$(CACHE)"

smoke: run-example
	@cat "$(OUTPUT)"

compose-up:
	$(COMPOSE) up -d postgres

compose-down:
	$(COMPOSE) down

compose-reset:
	$(COMPOSE) down -v

compose-logs:
	$(COMPOSE) logs -f postgres

compose-ps:
	$(COMPOSE) ps

postgres-dsn:
	@echo "$(POSTGRES_DSN)"

dev-standalone: sync-example
	$(GO) run $(CMD) standalone --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --state "$(STATE)" --cache "$(CACHE)" --wait "$(WAIT)"

dev-standalone-postgres: compose-up sync-example
	$(GO) run $(CMD) standalone --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate --wait "$(WAIT)"

dev-trigger: compose-up sync-example
	$(GO) run $(CMD) trigger --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate --wait "$(WAIT)"

dev-api: compose-up
	$(GO) run $(CMD) api --addr "$(ADDR)" --store "$(STORE)" --catalog "$(CATALOG)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

dev-worker: compose-up
	$(GO) run $(CMD) worker --store "$(STORE)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate

worker-once: compose-up
	$(GO) run $(CMD) worker --store "$(STORE)" --cache "$(CACHE)" --state-backend postgres --database-url "$(POSTGRES_DSN)" --migrate --once

windforce-register:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty register --name "$(WF_GIT_SOURCE_ID)" --repo-url "$(WF_REPO_URL)" --branch "$(WF_BRANCH)" --subpath "$(WF_SUBPATH)" --token-env "$(WF_GIT_TOKEN_ENV)"

windforce-sync:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty sync --git-source-id "$(WF_GIT_SOURCE_ID)"

windforce-schema:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty schema --app "$(WF_APP)" --action "$(WF_ACTION)"

windforce-openapi:
	python tools/windforce_control.py --api-url "$(WF_API_URL)" --workspace "$(WF_WORKSPACE)" --pretty openapi --app "$(WF_APP)"

clean:
	rm -rf "$(WFL_TMP)"
