.PHONY: lint lint-contrib lint-examples lint-all test conformance-test build build-contrib build-examples build-all fuzz fuzz-run fuzz-shard fuzz-smoke fuzz-full bench-smoke bench-full bench-compare bench-fs gnu-test compat-docker-build compat-docker-run website-dev release release-check release-snapshot fix-modules tag-release bats-test ensure-bash ensure-bats ensure-diffutils nix-build nix-cache

GO_CORE_PACKAGES := ./...
GO_CONTRIB_PACKAGES := ./contrib/awk/... ./contrib/extras/... ./contrib/htmltomarkdown/... ./contrib/jq/... ./contrib/python/... ./contrib/sqlite3/... ./contrib/yq/...
GO_EXAMPLES_PACKAGES := ./examples/...
GO_PACKAGES := $(GO_CORE_PACKAGES) $(GO_CONTRIB_PACKAGES) $(GO_EXAMPLES_PACKAGES)
BENCH_PACKAGES := ./internal/runtime ./cmd/gbash ./contrib/jq

FUZZTIME ?= 10s
FUZZ_SMOKE_TIME ?= 3s
FUZZ_DEEP_TIME ?= 15s
GORELEASER_VERSION ?= v2.14.3
GOLANGCI_LINT_VERSION ?= v2.11.3
GOLANGCI_LINT_BASE := GOTOOLCHAIN=go1.26.1 CGO_ENABLED=0 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
CUSTOM_GCL := $(CURDIR)/custom-gcl
CUSTOM_GCL_REPO ?= ewhauser/gbash
GOLANGCI_LINT := $(if $(wildcard .custom-gcl.yml),$(CUSTOM_GCL),$(GOLANGCI_LINT_BASE))
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
BENCH_SMOKE_REGEX ?= Benchmark(NewSession|RuntimeRunSimpleScript|SessionExecWarmSimpleScript|WorkflowCodebaseExploration|CommandGrepRecursive|CLIBinary|CommandJQTransform)$$
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
	FuzzDdCommand \
	FuzzCompatPredicateCommands \
	FuzzDirectoryTraversalCommands \
	FuzzCsplitCommand \
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
	FuzzDdCommand \
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
	FuzzNumfmtCommand \
	FuzzTextCommands \
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

FUZZ_FULL_SHARD_5 := \
	FuzzFoldCommand \
	./shell/syntax:FuzzParseAdversarial \
	./shell/syntax:FuzzParseAttackMutations

FUZZ_FULL_TARGETS := \
	$(FUZZ_FULL_SHARD_1) \
	$(FUZZ_FULL_SHARD_2) \
	$(FUZZ_FULL_SHARD_3) \
	$(FUZZ_FULL_SHARD_4) \
	$(FUZZ_FULL_SHARD_5)

# Depend on local plugin sources referenced via "path:" in .custom-gcl.yml so
# the binary rebuilds when they change.
CUSTOM_GCL_LOCAL_PLUGIN_DIRS := $(shell grep '^ *path:' .custom-gcl.yml 2>/dev/null | sed 's/^ *path: *//')
CUSTOM_GCL_LOCAL_DEPS := $(foreach d,$(CUSTOM_GCL_LOCAL_PLUGIN_DIRS),$(shell find $(d) -type f -name '*.go' 2>/dev/null | sort))
$(CUSTOM_GCL): .custom-gcl.yml $(CUSTOM_GCL_LOCAL_DEPS)
	@hash=$$(cat .custom-gcl.yml $(CUSTOM_GCL_LOCAL_DEPS) | if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi | cut -c1-12); \
	tag="custom-gcl/$${hash}"; \
	asset="custom-gcl-$$(go env GOOS)-$$(go env GOARCH).tar.gz"; \
	echo "==> downloading custom golangci-lint ($${tag})"; \
	if gh release download "$${tag}" --repo "$(CUSTOM_GCL_REPO)" --pattern "$${asset}" --dir "$(CURDIR)" 2>/dev/null; then \
		tar xzf "$(CURDIR)/$${asset}" -C "$(CURDIR)" && rm -f "$(CURDIR)/$${asset}"; \
	else \
		echo "==> release not found, building from source"; \
		$(GOLANGCI_LINT_BASE) custom; \
	fi
	@chmod +x $(CUSTOM_GCL)

lint: $(if $(wildcard .custom-gcl.yml),$(CUSTOM_GCL))
	@echo "==> lint ."; \
	$(GOLANGCI_LINT) run ./...

lint-contrib: $(if $(wildcard .custom-gcl.yml),$(CUSTOM_GCL))
	@set -eu; \
	for dir in $$($(LINT_MODULE_DIRS_CMD) | grep '/contrib/'); do \
		echo "==> lint $$dir"; \
		( cd "$$dir" && $(GOLANGCI_LINT) run ./... ); \
	done

lint-examples: $(if $(wildcard .custom-gcl.yml),$(CUSTOM_GCL))
	@set -eu; \
	for dir in $$($(LINT_MODULE_DIRS_CMD) | grep '/examples'); do \
		echo "==> lint $$dir"; \
		( cd "$$dir" && $(GOLANGCI_LINT) run ./... ); \
	done

lint-all: lint lint-contrib lint-examples

lint-new:
	@set -eu; \
	for dir in $$($(LINT_MODULE_DIRS_CMD)); do \
		echo "==> lint-new $$dir"; \
		( cd "$$dir" && $(GOLANGCI_LINT) run --new-from-rev=HEAD ./... ); \
	done

RACE := $(if $(CI),-race,$(if $(filter Darwin,$(shell uname -s)),,-race))

test:
	@DIFF_PATH=$$(./scripts/ensure-diffutils.sh) || exit 1; \
	GBASH_CONFORMANCE_DIFF="$$DIFF_PATH" go test $(RACE) $(GO_PACKAGES)

CONFORMANCE_RUN ?= TestConformance

conformance-test:
	@BASH_PATH=$$(./scripts/ensure-bash.sh) || exit 1; \
	BASH_VERSION_LINE=$$($$BASH_PATH --version | sed -n '1p') || exit 1; \
	GBASH_RUN_CONFORMANCE=1 GBASH_CONFORMANCE_BASH="$$BASH_PATH" \
	GBASH_CONFORMANCE_BASH_VERSION_LINE="$$BASH_VERSION_LINE" \
	  go test ./internal/conformance -run "$(CONFORMANCE_RUN)" -count=1 -timeout=20m

ensure-bash:
	@./scripts/ensure-bash.sh

ensure-diffutils:
	@./scripts/ensure-diffutils.sh

build:
	go build $(GO_CORE_PACKAGES)

build-contrib:
	go build $(GO_CONTRIB_PACKAGES)

build-examples:
	go build $(GO_EXAMPLES_PACKAGES)

build-all: build build-contrib build-examples

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
	GOTOOLCHAIN=go1.26.1 go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

release-snapshot:
	GOTOOLCHAIN=go1.26.1 go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean

fix-modules:
	./scripts/fix_modules.sh $(MODULE_VERSION)

tag-release:
	PUSH='$(PUSH_TAGS)' REMOTE='$(TAG_REMOTE)' ./scripts/tag_release.sh $(RELEASE_VERSION)

bats-test:
	@BATS_PATH=$$(./scripts/ensure-bats.sh) || exit 1; \
	go build -o scripts/tests/.gbash-test-bin ./cmd/gbash/ && \
	"$$BATS_PATH" scripts/tests/

ensure-bats:
	@./scripts/ensure-bats.sh

NIX_CACHE_BUCKET ?= gbash-nix-cache
NIX_CACHE_ENDPOINT ?= aea0a2c4e5c5c74a3e84a12c855a7e37.r2.cloudflarestorage.com
NIX_CACHE_PROFILE ?= r2
NIX_SECRET_KEY ?= $(HOME)/.config/nix/secret-key.pem

nix-build:
	nix build .#coreutils-test-suite

nix-cache: nix-build
	nix copy --to "s3://$(NIX_CACHE_BUCKET)?profile=$(NIX_CACHE_PROFILE)&endpoint=$(NIX_CACHE_ENDPOINT)" \
		--secret-key-files "$(NIX_SECRET_KEY)" ./result
