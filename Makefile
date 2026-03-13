.PHONY: lint test build run fuzz fuzz-run fuzz-shard fuzz-smoke fuzz-full bench-smoke bench-full gnu-test gnu-test-setup gnu-build-cache-fetch gnu-build-cache-publish release-check release-snapshot

GO_PACKAGES := ./... ./contrib/extras/... ./contrib/sqlite3/... ./contrib/jq/... ./contrib/yq/... ./examples/...
BENCH_PACKAGES := ./runtime ./contrib/jq

FUZZTIME ?= 10s
FUZZ_SMOKE_TIME ?= 3s
GORELEASER_VERSION ?= v2.14.3
BENCH_SMOKE_COUNT ?= 8
BENCH_SMOKE_TIME ?= 100ms
BENCH_FULL_COUNT ?= 10
BENCH_FULL_TIME ?= 200ms
BENCH_SMOKE_REGEX ?= Benchmark(NewSession|RuntimeRunSimpleScript|SessionExecWarmSimpleScript|WorkflowCodebaseExploration|CommandRGRecursive|CommandJQTransform)$$
GNU_CACHE_DIR ?= .cache/gnu
GNU_GBASH_BIN ?= $(GNU_CACHE_DIR)/bin/gbash
GNU_RESULTS_DIR ?=
GNU_FORCE_REBUILD ?=
GNU_BUILD_CACHE_REPO ?= ewhauser/gbash
GNU_BUILD_CACHE_TAG ?= gnu-build-cache-v1
GNU_BUILD_CACHE_VERSION ?= v1

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
	FuzzTextSearchCommands \
	FuzzTSortCommand

FUZZ_SMOKE_SHARD_DATA := \
	FuzzArchiveCommands \
	./contrib/sqlite3:FuzzSQLiteCommands \
	./contrib/yq:FuzzYQCommands \
	./contrib/jq:FuzzJQCommands

FUZZ_SMOKE_SHARD_SECURITY := \
	FuzzGeneratedPrograms \
	FuzzAttackMutations \
	FuzzEchoCommand \
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
	FuzzTextSearchCommands \
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
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...
	cd examples && golangci-lint run ./...
	cd contrib/extras && golangci-lint run ./...
	cd contrib/sqlite3 && golangci-lint run ./...
	cd contrib/yq && golangci-lint run ./...
	cd contrib/jq && golangci-lint run ./...

test:
	go test $(GO_PACKAGES)

build:
	go build $(GO_PACKAGES)

run:
	go run ./cmd/gbash

fuzz: fuzz-full

fuzz-run:
	@test -n "$(strip $(FUZZ_TARGETS))" || { echo "FUZZ_TARGETS is required"; exit 1; }
	@set -e; \
	for target in $(FUZZ_TARGETS); do \
		pkg=./runtime; \
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

gnu-test-setup:
	mkdir -p $(GNU_CACHE_DIR)/bin
	go run ./cmd/gbash-gnu --cache-dir $(GNU_CACHE_DIR) --setup

gnu-build-cache-fetch:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh fetch

gnu-build-cache-publish:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh publish

gnu-test:
	GNU_CACHE_DIR='$(GNU_CACHE_DIR)' GNU_GBASH_BIN='$(GNU_GBASH_BIN)' GNU_RESULTS_DIR='$(GNU_RESULTS_DIR)' GNU_UTILS='$(GNU_UTILS)' GNU_TESTS='$(GNU_TESTS)' GNU_KEEP_WORKDIR='$(GNU_KEEP_WORKDIR)' GNU_FORCE_REBUILD='$(GNU_FORCE_REBUILD)' GNU_BUILD_CACHE_REPO='$(GNU_BUILD_CACHE_REPO)' GNU_BUILD_CACHE_TAG='$(GNU_BUILD_CACHE_TAG)' GNU_BUILD_CACHE_VERSION='$(GNU_BUILD_CACHE_VERSION)' ./scripts/gnu-build-cache.sh run

release-check:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean
