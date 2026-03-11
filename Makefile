.PHONY: lint test build fuzz release-check release-snapshot

FUZZTIME ?= 10s
GORELEASER_VERSION ?= v2.14.3

lint:
	@which golangci-lint > /dev/null || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

test:
	go test ./...

build:
	go build ./...

fuzz:
	go test ./runtime -run=^$$ -fuzz=FuzzRuntimeScript -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzMalformedScript -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSessionSequence -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzFilePathCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDirectoryTraversalCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTextSearchCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCPFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCutFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzUniqFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzEnvCommandFlags -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTRFlagsCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzTimeoutCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzFileCommandFlags -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCommCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzCatCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzBasenameCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzBase64Command -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDiffCommand -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzShellProcessCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzNestedShellCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzDataCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzYQCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSQLiteCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzSQLiteFileCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzArchiveCommands -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzGeneratedPrograms -fuzztime=$(FUZZTIME)
	go test ./runtime -run=^$$ -fuzz=FuzzAttackMutations -fuzztime=$(FUZZTIME)

release-check:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) check

release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION) release --snapshot --clean
