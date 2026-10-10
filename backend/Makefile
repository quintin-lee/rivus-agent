MODULE := rivus-agent-backend
BIN := bin/agentd
MAIN := ./cmd/agentd
GOFLAGS ?=

.PHONY: help build test test-integration test-eval bench vet tidy run clean

help:
	@echo "build            - compile to $(BIN)"
	@echo "test             - run all tests (unit + integration + evals)"
	@echo "test-integration - run API/approval integration tests only"
	@echo "test-eval        - run regression eval scenarios only"
	@echo "bench            - run benchmarks + load smoke"
	@echo "vet              - go vet"
	@echo "tidy             - go mod tidy"
	@echo "run              - run locally (needs MODEL_* env)"
	@echo "clean            - remove build artifacts"

build:
	mkdir -p bin
	go build $(GOFLAGS) -trimpath -o $(BIN) $(MAIN)

test:
	go test ./...

test-integration:
	go test ./tests/integration/ -count=1

test-eval:
	go test ./tests/evals/ -count=1

bench:
	go test ./tests/bench/ -count=1 -run=NONE -bench=. -benchtime=50x

vet:
	go vet ./...

tidy:
	go mod tidy

run:
	go run $(MAIN) -config $${AGENT_CONFIG:-configs/config.example.yaml}

clean:
	rm -rf bin
