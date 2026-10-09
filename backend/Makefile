MODULE := rivus-agent-backend
BIN := bin/agentd
MAIN := ./cmd/agentd
GOFLAGS ?=

.PHONY: help build test vet tidy run clean

help:
	@echo "build  - compile to $(BIN)"
	@echo "test   - run all unit tests"
	@echo "vet    - go vet"
	@echo "tidy   - go mod tidy"
	@echo "run    - run locally (needs MODEL_* env)"
	@echo "clean  - remove build artifacts"

build:
	mkdir -p bin
	go build $(GOFLAGS) -trimpath -o $(BIN) $(MAIN)

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run:
	go run $(MAIN) -config $${AGENT_CONFIG:-configs/config.example.yaml}

clean:
	rm -rf bin
