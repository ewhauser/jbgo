.PHONY: lint test build fuzz bench-smoke bench-full gnu-test gnu-test-setup release-check release-snapshot

GO_PACKAGES := ./... ./examples/...

FUZZTIME ?= 10s
GORELEASER_VERSION ?= v2.14.3
BENCH_SMOKE_COUNT ?= 8
BENCH_SMOKE_TIME ?= 100ms
BENCH_FULL_COUNT ?= 10
BENCH_FULL_TIME ?= 200ms
BENCH_SMOKE_REGEX ?= Benchmark(NewSession|RuntimeRunSimpleScript|SessionExecWarmSimpleScript|WorkflowCodebaseExploration|CommandRGRecursive|CommandJQTransform)$$
GNU_CACHE_DIR ?= .cache/gnu
GNU_JBGO_BIN ?= $(GNU_CACHE_DIR)/bin/jbgo
GNU_RESULTS_DIR ?=

lint:
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run $(GO_PACKAGES)

test:
	go test $(GO_PACKAGES)

build:
	go build $(GO_PACKAGES)

fuzz:
	go test ./runtime -run=^$$ -fuzz=FuzzRuntimeScript -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzMalformedScript -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSessionSequence -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzFilePathCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDirectoryTraversalCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTextSearchCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCPFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzMVFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzNLFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzPasteFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSedFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSortFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzXArgsFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCutFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzFindFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzGrepFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCurlFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzLSFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzUniqFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzEnvCommandFlags -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTRFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTimeoutCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzFileCommandFlags -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCommCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCatCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzBase32Command -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzBasenameCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzBase64Command -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDiffCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzExprCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzShellProcessCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzNestedShellCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDataCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzJQCompatibilityFlags -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzYQCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSQLiteCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSQLiteFileCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzArchiveCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzGeneratedPrograms -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzAttackMutations -fuzztime=$(FUZZTIME)

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
