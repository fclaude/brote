## check: what a change needs before it is called done: format, vet, staticcheck when installed, race tests
check: lint test

build:
	go build -o bin/brote ./cmd/brote
	go build -o bin/brote-lsp ./cmd/brote-lsp

lint:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	@if command -v staticcheck >/dev/null; then staticcheck ./...; else echo "staticcheck is not installed; skipped"; fi

test:
	go test -race -count=1 ./...

.PHONY: check build lint test
