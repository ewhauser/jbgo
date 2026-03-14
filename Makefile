.PHONY: lint test build fuzz fuzz-run fuzz-shard fuzz-smoke fuzz-full bench-smoke bench-full bench-compare gnu-test gnu-test-setup gnu-build-cache-fetch gnu-build-cache-publish compat-docker-build compat-docker-run release release-check release-snapshot fix-modules tag-release

GO_PACKAGES := ./... ./contrib/awk/... ./contrib/extras/... ./contrib/sqlite3/... ./contrib/jq/... ./contrib/yq/... ./examples/...
BENCH_PACKAGES := ./internal/runtime ./cmd/gbash ./contrib/jq

FUZZTIME ?= 10s
FUZZ_SMOKE_TIME ?= 3s
GORELEASER_VERSION ?= v2.14.3
GOLANGCI_LINT_VERSION ?= v2.11.3
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
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
JUST_BASH_SPEC ?= just-bash@2.13.0
JSON_OUT ?=
GNU_CACHE_DIR ?= .cache/gnu
GNU_GBASH_BIN ?= $(GNU_CACHE_DIR)/bin/gbash
GNU_RESULTS_DIR ?=
GNU_FORCE_REBUILD ?=
GNU_BUILD_CACHE_REPO ?= ewhauser/gbash
GNU_BUILD_CACHE_TAG ?= gnu-build-cache-v3
GNU_BUILD_CACHE_VERSION ?= v3

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
	$(GOLANGCI_LINT) run ./...
	cd examples && $(GOLANGCI_LINT) run ./...
	cd contrib/awk && $(GOLANGCI_LINT) run ./...
	cd contrib/extras && $(GOLANGCI_LINT) run ./...
	cd contrib/sqlite3 && $(GOLANGCI_LINT) run ./...
	cd contrib/yq && $(GOLANGCI_LINT) run ./...
	cd contrib/jq && $(GOLANGCI_LINT) run ./...

test:
	go test $(GO_PACKAGES)

build:
	go build $(GO_PACKAGES)

fuzz: fuzz-full

fuzz-run:
	@test -n "$(strip $(FUZZ_TARGETS))" || { echo "FUZZ_TARGETS is required"; exit 1; }
	@set -e; \
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
		go test $$pkg -run=^$$ -fuzz=$$fuzz_target -fuzztime=$(FUZZTIME); \
	done

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

gnu-test-setup:
	mkdir -p $(GNU_CACHE_DIR)/bin
	go run ./cmd/gbash-gnu --cache-dir $(GNU_CACHE_DIR) --setup

gnu-build-cache-fetch:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh fetch

gnu-build-cache-publish:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh publish

gnu-test:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_RESULTS_DIR='$(GNU_RESULTS_DIR)' GNU_UTILS='$(GNU_UTILS)' GNU_TESTS='$(GNU_TESTS)' GNU_KEEP_WORKDIR='$(GNU_KEEP_WORKDIR)' GNU_FORCE_REBUILD='$(GNU_FORCE_REBUILD)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh run

compat-docker-build:
	./scripts/compat-docker-build.sh

compat-docker-run:
	./scripts/compat-docker-run.sh

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
