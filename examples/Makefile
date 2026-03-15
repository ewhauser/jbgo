PROJECT ?= $(shell gcloud config get-value project 2>/dev/null)
LOCATION ?= us-west1
MODEL ?= gemini-2.5-flash
SQLITE_FS_DB ?= /tmp/gbash-sandbox.db
SQLITE_FS_SCRIPT ?= pwd

.PHONY: run run-custom-zstd run-oauth-network-extension run-otel run-sqlite-backed-fs run-sqlite-backed-fs-repl run-transactional-workspaces run-vertex-bash-chat
run: run-vertex-bash-chat

run-custom-zstd:
	go run ./custom-zstd < ./custom-zstd/demo.sh

run-oauth-network-extension:
	@echo "Running oauth-network-extension demo"
	@go run ./oauth-network-extension

run-otel:
	@echo "Running otel demo"
	@go run ./otel

run-vertex-bash-chat:
	@if ! command -v gcloud >/dev/null 2>&1; then \
		echo "gcloud is required for make run"; \
		exit 1; \
	fi
	@if [ -z "$(PROJECT)" ]; then \
		echo "No gcloud project is configured. Run: gcloud config set project YOUR_PROJECT_ID"; \
		exit 1; \
	fi
	@if ! gcloud auth application-default print-access-token >/dev/null 2>&1; then \
		echo "No Application Default Credentials found. Running gcloud auth application-default login..."; \
		gcloud auth application-default login; \
	fi
	@echo "Running adk-bash-chat with project $(PROJECT) in $(LOCATION)"
	@GOOGLE_CLOUD_PROJECT="$(PROJECT)" \
	GOOGLE_CLOUD_LOCATION="$(LOCATION)" \
	go run ./adk-bash-chat --backend=vertex --model="$(MODEL)"

run-sqlite-backed-fs:
	@echo "Running sqlite-backed-fs with backing DB $(SQLITE_FS_DB)"
	@go run ./sqlite-backed-fs --db "$(SQLITE_FS_DB)" --script "$(SQLITE_FS_SCRIPT)"

run-sqlite-backed-fs-repl:
	@echo "Running sqlite-backed-fs REPL with backing DB $(SQLITE_FS_DB)"
	@go run ./sqlite-backed-fs --db "$(SQLITE_FS_DB)" --repl

run-transactional-workspaces:
	@echo "Running transactional-workspaces demo"
	@go run ./transactional-workspaces
