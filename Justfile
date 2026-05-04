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

print-cdp-domains input:
    go run ./tools/cdpgen -input {{input}} -print-domains

render-cdp-domain input domain:
    go run ./tools/cdpgen -input {{input}} -render-domain {{domain}}

gen-cdp input output domains:
    go run ./tools/cdpgen -input {{input}} -generate-domains {{domains}} -out {{output}}

refresh-cdp-protocol:
    curl -fsSL https://raw.githubusercontent.com/ChromeDevTools/devtools-protocol/master/json/browser_protocol.json -o tools/cdpgen/testdata/browser_protocol.json
    curl -fsSL https://raw.githubusercontent.com/ChromeDevTools/devtools-protocol/master/json/js_protocol.json -o tools/cdpgen/testdata/js_protocol.json

gen-cdp-seed:
    go run ./tools/cdpgen -input tools/cdpgen/testdata/browser_protocol.json,tools/cdpgen/testdata/js_protocol.json -generate-domains Accessibility,Browser,CSS,DOM,DOMSnapshot,Emulation,Fetch,IO,Input,Log,Network,Overlay,Page,Performance,Runtime,Security,Storage,Target -out cdp/proto

check: fmt-check vet lint test-race build
