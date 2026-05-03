default:
    @just --list --unsorted

setup:
    @command -v lefthook >/dev/null 2>&1 || go install github.com/evilmartians/lefthook@latest
    lefthook install
    just sync-tools

sync-tools:
    go mod tidy -modfile=tools.go.mod

build:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to build"; else go build ./...; fi

test:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to test"; else go test ./...; fi

test-race:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to test"; else go test -race ./...; fi

test-sum:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to test"; else go tool -modfile=tools.go.mod gotestsum --format testname -- ./...; fi

fmt:
    gofmt -w .

fmt-check:
    @test -z "$(gofmt -l .)" || (echo "gofmt needed on:"; gofmt -l .; exit 1)

vet:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to vet"; else go vet ./...; fi

lint:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to lint"; else go tool -modfile=tools.go.mod golangci-lint run; fi

tidy:
    @pkgs=$(go list ./... 2>/dev/null); if [ -z "$pkgs" ]; then echo "no packages to tidy"; else go mod tidy; fi

tidy-all: tidy sync-tools

check: fmt-check vet lint test-race build
