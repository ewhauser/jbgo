.PHONY: lint test build fuzz

FUZZTIME ?= 10s

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
