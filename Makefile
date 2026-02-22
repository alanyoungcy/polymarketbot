.PHONY: build build-backend run-backend test lint race proto migrate clean

GO       := go
BIN      := bin
BACKEND  := $(BIN)/polybot
PROTO_DIR := proto
GEN_DIR  := internal/pb

build: build-backend

build-backend:
	$(GO) build -o $(BACKEND) ./cmd/polybot

run-backend:
	$(GO) run ./cmd/polybot --config config.toml

test:
	$(GO) test ./...

lint:
	golangci-lint run ./...

race:
	$(GO) test -race ./...

# proto: generate Go from .proto files. Requires buf (go install github.com/bufbuild/buf/cmd/buf@latest)
# or: protoc with protoc-gen-go (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest).
proto:
	buf generate

proto-lint:
	buf lint

migrate:
	@echo "migrate target is not implemented in cmd/polybot; migrations run automatically on startup when supabase.run_migrations=true"

clean:
	rm -rf $(BIN)

deps:
	$(GO) mod tidy
