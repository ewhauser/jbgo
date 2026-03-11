.PHONY: lint test build fuzz fuzz-run fuzz-shard fuzz-smoke fuzz-full bench-smoke bench-full gnu-test gnu-test-setup release-check release-snapshot

GO_PACKAGES := ./... ./examples/...

FUZZTIME ?= 10s
FUZZ_SMOKE_TIME ?= 3s
GORELEASER_VERSION ?= v2.14.3
BENCH_SMOKE_COUNT ?= 8
BENCH_SMOKE_TIME ?= 100ms
BENCH_FULL_COUNT ?= 10
BENCH_FULL_TIME ?= 200ms
BENCH_SMOKE_REGEX ?= Benchmark(NewSession|RuntimeRunSimpleScript|SessionExecWarmSimpleScript|WorkflowCodebaseExploration|CommandRGRecursive|CommandJQTransform)$$
GNU_CACHE_DIR ?= .cache/gnu
GNU_JBGO_BIN ?= $(GNU_CACHE_DIR)/bin/jbgo
GNU_RESULTS_DIR ?=

FUZZ_SMOKE_SHARD_CORE := \
	FuzzRuntimeScript \
	FuzzMalformedScript \
	FuzzSessionSequence

FUZZ_SMOKE_SHARD_PATHS := \
	FuzzFilePathCommands \
	FuzzDirectoryTraversalCommands \
	FuzzTextSearchCommands

FUZZ_SMOKE_SHARD_DATA := \
	FuzzSQLiteCommands \
	FuzzArchiveCommands \
	FuzzYQCommands

FUZZ_SMOKE_SHARD_SECURITY := \
	FuzzGeneratedPrograms \
	FuzzAttackMutations \
	FuzzShellProcessCommands

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
	FuzzJQCompatibilityFlags \
	FuzzArchiveCommands

FUZZ_FULL_SHARD_2 := \
	FuzzMalformedScript \
	FuzzFilePathCommands \
	FuzzMVFlagsCommand \
	FuzzPasteFlagsCommand \
	FuzzFindFlagsCommand \
	FuzzEnvCommandFlags \
	FuzzCommCommand \
	FuzzBase32Command \
	FuzzBase64Command \
	FuzzYQCommands

FUZZ_FULL_SHARD_3 := \
	FuzzDirectoryTraversalCommands \
	FuzzTextSearchCommands \
	FuzzSedFlagsCommand \
	FuzzXArgsFlagsCommand \
	FuzzGrepFlagsCommand \
	FuzzTRFlagsCommand \
	FuzzCatCommand \
	FuzzDiffCommand \
	FuzzSQLiteCommands \
	FuzzGeneratedPrograms

FUZZ_FULL_SHARD_4 := \
	FuzzLSFlagsCommand \
	FuzzSortFlagsCommand \
	FuzzCurlFlagsCommand \
	FuzzTimeoutCommand \
	FuzzExprCommand \
	FuzzShellProcessCommands \
	FuzzNestedShellCommands \
	FuzzDataCommands \
	FuzzSQLiteFileCommands \
	FuzzAttackMutations

FUZZ_FULL_TARGETS := \
	$(FUZZ_FULL_SHARD_1) \
	$(FUZZ_FULL_SHARD_2) \
	$(FUZZ_FULL_SHARD_3) \
	$(FUZZ_FULL_SHARD_4)

lint:
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run $(GO_PACKAGES)

test:
	go test $(GO_PACKAGES)

build:
	go build $(GO_PACKAGES)

fuzz: fuzz-full

fuzz-run:
	@test -n "$(strip $(FUZZ_TARGETS))" || { echo "FUZZ_TARGETS is required"; exit 1; }
	@set -e; \
	for target in $(FUZZ_TARGETS); do \
		echo "==> $$target"; \
		go test ./runtime -run=^$$ -fuzz=$$target -fuzztime=$(FUZZTIME); \
	done

fuzz-shard:
	@test -n "$(FUZZ_SHARD)" || { echo "FUZZ_SHARD is required"; exit 1; }
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(strip $($(FUZZ_SHARD)))" FUZZTIME="$(FUZZTIME)"

fuzz-smoke:
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(FUZZ_SMOKE_TARGETS)" FUZZTIME="$(FUZZ_SMOKE_TIME)"

fuzz-full:
	@$(MAKE) --no-print-directory fuzz-run FUZZ_TARGETS="$(FUZZ_FULL_TARGETS)" FUZZTIME="$(FUZZTIME)"

bench-smoke:
	@go test ./runtime -run=^$$ -bench '$(BENCH_SMOKE_REGEX)' -benchmem -count=$(BENCH_SMOKE_COUNT) -benchtime=$(BENCH_SMOKE_TIME)

bench-full:
	@go test ./runtime -run=^$$ -bench . -benchmem -count=$(BENCH_FULL_COUNT) -benchtime=$(BENCH_FULL_TIME)

gnu-test-setup:
	mkdir -p $(GNU_CACHE_DIR)/bin
	go run ./cmd/jbgo-gnu --cache-dir $(GNU_CACHE_DIR) --setup

gnu-test:
	mkdir -p $(GNU_CACHE_DIR)/bin
	go build -o $(GNU_JBGO_BIN) ./cmd/jbgo
	GNU_UTILS='$(GNU_UTILS)' GNU_TESTS='$(GNU_TESTS)' GNU_KEEP_WORKDIR='$(GNU_KEEP_WORKDIR)' GNU_RESULTS_DIR='$(GNU_RESULTS_DIR)' go run ./cmd/jbgo-gnu --cache-dir $(GNU_CACHE_DIR) --jbgo-bin $(GNU_JBGO_BIN) $(if $(GNU_RESULTS_DIR),--results-dir $(GNU_RESULTS_DIR),)

release-check:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean
