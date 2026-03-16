.PHONY: lint test conformance-test build fuzz fuzz-run fuzz-shard fuzz-smoke fuzz-full bench-smoke bench-full bench-compare bench-fs gnu-test compat-docker-build compat-docker-run website-dev release release-check release-snapshot fix-modules tag-release

GO_PACKAGES := ./... ./contrib/awk/... ./contrib/extras/... ./contrib/htmltomarkdown/... ./contrib/sqlite3/... ./contrib/jq/... ./contrib/yq/... ./examples/...
BENCH_PACKAGES := ./internal/runtime ./cmd/gbash ./contrib/jq

FUZZTIME ?= 10s
FUZZ_SMOKE_TIME ?= 3s
FUZZ_DEEP_TIME ?= 15s
GORELEASER_VERSION ?= v2.14.3
GOLANGCI_LINT_VERSION ?= v2.11.3
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
# Discover every main module in the active go.work so local lint matches CI.
LINT_MODULE_DIRS_CMD = go list -m -f '{{if .Main}}{{.Dir}}{{end}}' all
GH ?= gh
MODULE_VERSION ?=
RELEASE_VERSION ?=
RELEASE_REF ?= main
RELEASE_WORKFLOW ?= prepare-release.yml
TAG_REMOTE ?= origin
PUSH_TAGS ?= 0
BENCH_SMOKE_COUNT ?= 8
BENCH_SMOKE_TIME ?= 100ms
BENCH_FULL_COUNT ?= 10
BENCH_FULL_TIME ?= 200ms
BENCH_SMOKE_REGEX ?= Benchmark(NewSession|RuntimeRunSimpleScript|SessionExecWarmSimpleScript|WorkflowCodebaseExploration|CommandRGRecursive|CLIBinary|CommandJQTransform)$$
BENCH_COMPARE_RUNS ?= 100
BENCH_FS_RUNS ?= 50
BENCH_FS_JSON_OUT ?= website/content/performance/filesystem-benchmark-data.json
JUST_BASH_SPEC ?= just-bash@2.13.0
JSON_OUT ?=
GNU_CACHE_DIR ?= .cache/gnu
GNU_RESULTS_DIR ?=
COMPAT_DOCKER_IMAGE ?= gbash-compat-local
COMPAT_DOCKER_BASE_IMAGE ?= ghcr.io/ewhauser/gbash-compat:latest
COMPAT_DOCKER_PLATFORM ?=
COMPAT_DOCKER_PULL ?= always
WEBSITE_PNPM ?= npx --yes pnpm@10.32.1
GBASH_WEBSITE_REMOTE_COMPAT_BASE_URL ?= https://ewhauser.github.io/gbash/compat/latest
GBASH_WEBSITE_REMOTE_COMPAT_SUMMARY_URL ?= $(GBASH_WEBSITE_REMOTE_COMPAT_BASE_URL)/summary.json
GBASH_WEBSITE_REMOTE_COMPAT_BADGE_URL ?= $(GBASH_WEBSITE_REMOTE_COMPAT_BASE_URL)/badge.svg

FUZZ_SMOKE_SHARD_CORE := \
	FuzzRuntimeScript \
	FuzzMalformedScript \
	FuzzSessionSequence

FUZZ_SMOKE_SHARD_PATHS := \
	FuzzFilePathCommands \
	FuzzRealpathCommand \
	FuzzTruncateCommand \
	FuzzCompatPredicateCommands \
	FuzzDirectoryTraversalCommands \
	FuzzCsplitCommand \
	FuzzTextSearchCommands \
	FuzzTSortCommand

FUZZ_SMOKE_SHARD_DATA := \
	FuzzArchiveCommands \
	FuzzNumfmtCommand \
	./contrib/sqlite3:FuzzSQLiteCommands \
	./contrib/yq:FuzzYQCommands \
	./contrib/jq:FuzzJQCommands

FUZZ_SMOKE_SHARD_SECURITY := \
	FuzzGeneratedPrograms \
	FuzzAttackMutations \
	FuzzEchoCommand \
	FuzzUnameCommand \
	FuzzWhoCommand \
	FuzzShellProcessCommands \
	FuzzDircolorsCommand

FUZZ_SMOKE_TARGETS := \
	$(FUZZ_SMOKE_SHARD_CORE) \
	$(FUZZ_SMOKE_SHARD_PATHS) \
	$(FUZZ_SMOKE_SHARD_DATA) \
	$(FUZZ_SMOKE_SHARD_SECURITY)

FUZZ_FULL_SHARD_1 := \
	FuzzRuntimeScript \
	FuzzSessionSequence \
	FuzzCPFlagsCommand \
	FuzzNLFlagsCommand \
	FuzzCutFlagsCommand \
	FuzzUniqFlagsCommand \
	FuzzFileCommandFlags \
	FuzzBasenameCommand \
	./contrib/jq:FuzzJQCompatibilityFlags \
	FuzzArchiveCommands

FUZZ_FULL_SHARD_2 := \
	FuzzMalformedScript \
	FuzzFilePathCommands \
	FuzzRealpathCommand \
	FuzzTruncateCommand \
	FuzzCompatPredicateCommands \
	./policy:FuzzCheckPathReadSymlinkPolicy \
	./policy:FuzzCheckPathWriteSymlinkPolicy \
	./fs:FuzzOverlayFSRealpath \
	FuzzMVFlagsCommand \
	FuzzPasteFlagsCommand \
	FuzzFindFlagsCommand \
	FuzzEnvCommandFlags \
	FuzzCommCommand \
	FuzzBase32Command \
	FuzzBase64Command \
	FuzzBasencCommand

FUZZ_FULL_SHARD_3 := \
	FuzzDirectoryTraversalCommands \
	FuzzCsplitCommand \
	FuzzTextSearchCommands \
	FuzzNumfmtCommand \
	FuzzColumnCommand \
	FuzzSedFlagsCommand \
	FuzzXArgsFlagsCommand \
	FuzzGrepFlagsCommand \
	FuzzTRFlagsCommand \
	FuzzCatCommand \
	FuzzDiffCommand \
	FuzzTarCommand \
	FuzzChecksumCommands \
	./contrib/sqlite3:FuzzSQLiteCommands \
	FuzzGeneratedPrograms

FUZZ_FULL_SHARD_4 := \
	FuzzLSFlagsCommand \
	FuzzSortFlagsCommand \
	FuzzTSortCommand \
	FuzzCurlFlagsCommand \
	FuzzTimeoutCommand \
	FuzzExprCommand \
	FuzzEchoCommand \
	FuzzUnameCommand \
	FuzzWhoCommand \
	FuzzDircolorsCommand \
	FuzzShellProcessCommands \
	FuzzNestedShellCommands \
	FuzzDataCommands \
	FuzzHostOverlaySymlinkPolicy \
	./network:FuzzHTTPClientPolicy \
	./contrib/sqlite3:FuzzSQLiteFileCommands \
	./contrib/yq:FuzzYQCommands \
	./contrib/jq:FuzzJQCommands \
	FuzzAttackMutations

FUZZ_FULL_TARGETS := \
	$(FUZZ_FULL_SHARD_1) \
	$(FUZZ_FULL_SHARD_2) \
	$(FUZZ_FULL_SHARD_3) \
	$(FUZZ_FULL_SHARD_4)

lint:
	@set -eu; \
	for dir in $$($(LINT_MODULE_DIRS_CMD)); do \
		echo "==> lint $$dir"; \
		( cd "$$dir" && $(GOLANGCI_LINT) run ./... ); \
	done

lint-new:
	@set -eu; \
	for dir in $$($(LINT_MODULE_DIRS_CMD)); do \
		echo "==> lint-new $$dir"; \
		( cd "$$dir" && $(GOLANGCI_LINT) run --new-from-rev=HEAD ./... ); \
	done

test:
	go test -race $(GO_PACKAGES)

conformance-test:
	GBASH_RUN_CONFORMANCE=1 go test ./internal/conformance -run TestConformance -count=1 -timeout=20m

build:
	go build $(GO_PACKAGES)

fuzz: fuzz-full

fuzz-run:
	@test -n "$(strip $(FUZZ_TARGETS))" || { echo "FUZZ_TARGETS is required"; exit 1; }
	@set -eu; \
	failed=""; \
	for target in $(FUZZ_TARGETS); do \
		pkg=./internal/runtime; \
		fuzz_target=$$target; \
		case "$$target" in \
			*:* ) \
				pkg=$${target%%:*}; \
				fuzz_target=$${target#*:}; \
				;; \
		esac; \
		echo "==> $$pkg $$fuzz_target"; \
		if ! go test $$pkg -run=^$$ -fuzz=$$fuzz_target -fuzztime=$(FUZZTIME); then \
			failed="$$failed $$target"; \
		fi; \
	done; \
	if [ -n "$$failed" ]; then \
		echo "fuzz failures:$$failed"; \
		exit 1; \
	fi

fuzz-shard:
	@test -n "$(FUZZ_SHARD)" || { echo "FUZZ_SHARD is required"; exit 1; }
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(strip $($(FUZZ_SHARD)))" FUZZTIME="$(FUZZTIME)"

fuzz-smoke:
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(FUZZ_SMOKE_TARGETS)" FUZZTIME="$(FUZZ_SMOKE_TIME)"

fuzz-full:
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(FUZZ_FULL_TARGETS)" FUZZTIME="$(FUZZTIME)"

bench-smoke:
	@go test $(BENCH_PACKAGES) -run=^$$ -bench '$(BENCH_SMOKE_REGEX)' -benchmem -count=$(BENCH_SMOKE_COUNT) -benchtime=$(BENCH_SMOKE_TIME)

bench-full:
	@go test $(BENCH_PACKAGES) -run=^$$ -bench . -benchmem -count=$(BENCH_FULL_COUNT) -benchtime=$(BENCH_FULL_TIME)

bench-compare:
	@set -eu; \
	if [ -n "$(JSON_OUT)" ]; then \
		go run ./scripts/bench-compare --runs "$(BENCH_COMPARE_RUNS)" --just-bash-spec "$(JUST_BASH_SPEC)" --json-out "$(JSON_OUT)"; \
	else \
		go run ./scripts/bench-compare --runs "$(BENCH_COMPARE_RUNS)" --just-bash-spec "$(JUST_BASH_SPEC)"; \
	fi

bench-fs:
	@set -eu; \
	go run ./examples/bench-fs --runs "$(BENCH_FS_RUNS)" --json-out "$(BENCH_FS_JSON_OUT)"

gnu-test:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_RESULTS_DIR='$(GNU_RESULTS_DIR)' GNU_UTILS='$(GNU_UTILS)' GNU_TESTS='$(GNU_TESTS)' GNU_KEEP_WORKDIR='$(GNU_KEEP_WORKDIR)' COMPAT_DOCKER_IMAGE='$(COMPAT_DOCKER_IMAGE)' COMPAT_DOCKER_BASE_IMAGE='$(COMPAT_DOCKER_BASE_IMAGE)' COMPAT_DOCKER_PLATFORM='$(COMPAT_DOCKER_PLATFORM)' COMPAT_DOCKER_PULL='$(COMPAT_DOCKER_PULL)' ./scripts/compat-docker-run.sh

compat-docker-build:
	COMPAT_DOCKER_IMAGE='$(COMPAT_DOCKER_IMAGE)' COMPAT_DOCKER_BASE_IMAGE='$(COMPAT_DOCKER_BASE_IMAGE)' COMPAT_DOCKER_PLATFORM='$(COMPAT_DOCKER_PLATFORM)' COMPAT_DOCKER_PULL='$(COMPAT_DOCKER_PULL)' ./scripts/compat-docker-build.sh

compat-docker-run:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_RESULTS_DIR='$(GNU_RESULTS_DIR)' GNU_UTILS='$(GNU_UTILS)' GNU_TESTS='$(GNU_TESTS)' GNU_KEEP_WORKDIR='$(GNU_KEEP_WORKDIR)' COMPAT_DOCKER_IMAGE='$(COMPAT_DOCKER_IMAGE)' COMPAT_DOCKER_BASE_IMAGE='$(COMPAT_DOCKER_BASE_IMAGE)' COMPAT_DOCKER_PLATFORM='$(COMPAT_DOCKER_PLATFORM)' COMPAT_DOCKER_PULL='$(COMPAT_DOCKER_PULL)' ./scripts/compat-docker-run.sh

website-dev:
	@set -eu; \
	compat_tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$compat_tmp"' EXIT INT TERM; \
	curl --fail --location --silent --show-error --output "$$compat_tmp/summary.json" "$(GBASH_WEBSITE_REMOTE_COMPAT_SUMMARY_URL)"; \
	curl --fail --location --silent --show-error --output "$$compat_tmp/badge.svg" "$(GBASH_WEBSITE_REMOTE_COMPAT_BADGE_URL)"; \
	echo "==> website dev with remote compat from $(GBASH_WEBSITE_REMOTE_COMPAT_BASE_URL)"; \
	status=0; \
	GBASH_WEBSITE_COMPAT_SUMMARY_PATH="$$compat_tmp/summary.json" \
	GBASH_WEBSITE_COMPAT_BADGE_PATH="$$compat_tmp/badge.svg" \
	$(WEBSITE_PNPM) --dir website dev || status=$$?; \
	if [ "$$status" -ne 0 ] && [ "$$status" -ne 130 ] && [ "$$status" -ne 143 ]; then \
		exit "$$status"; \
	fi

release:
	@command -v $(GH) > /dev/null || { echo "$(GH) CLI is required"; exit 1; }
	$(GH) workflow run $(RELEASE_WORKFLOW) --ref $(RELEASE_REF)

release-check:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean

fix-modules:
	./scripts/fix_modules.sh $(MODULE_VERSION)

tag-release:
	PUSH='$(PUSH_TAGS)' REMOTE='$(TAG_REMOTE)' ./scripts/tag_release.sh $(RELEASE_VERSION)
