# navigo -- Agent Guidelines

## What Is This Project?

A pure Go browser automation library for CDP-first agentic automation, scraping, and future BiDi support.

**This is a library first.** The MCP server is an adapter layer, not the center of the repository.

## Repository Structure

```
navigo/
├── browser.go          # High-level Browser lifecycle
├── context.go          # BrowserContext isolation and cookies
├── page.go             # Navigation, evaluation, screenshots, PDF
├── element.go          # Element handles and interactions
├── frame.go            # Frame tree and cross-frame operations
├── wait.go             # Auto-wait logic
├── selector.go         # Selector compilation and resolution
├── errors.go           # Typed public errors
├── options.go          # Functional options
├── cdp/                # Low-level CDP transport/session/protocol packages
├── ax/                 # Accessibility tree and agent-oriented APIs
├── launcher/           # Browser process discovery and startup
├── stealth/            # Anti-detection script injection
├── mcp/                # MCP server adapter
├── pool/               # Concurrency and rate-limited browser reuse
├── internal/           # Non-public support packages
├── examples/           # Runnable examples
└── testdata/           # HTML fixtures, protocol fixtures, golden snapshots
```

Keep exported surface small. Most packages should support the root `navigo` API, not become APIs themselves by accident.

## Key Conventions

- Prefer idiomatic Go over framework-shaped abstractions.
- No panics in library code. Return typed, wrapped errors.
- All networked or browser-bound operations should be `context.Context` aware.
- Element and frame handles must define invalidation semantics explicitly.
- Do not add dev tools to the main `go.mod`.
- Use `just` for common tasks and `lefthook` for git hooks.
- Conventional commits: `type(scope): description`, first line <= 72 chars.

## Error Wrapping

Use operation-scoped wrapping:

```go
return fmt.Errorf("navigate page: %w", err)
```

Avoid returning raw lower-level errors from public APIs without added context.

## Testing Expectations

- Prefer table-driven unit tests for protocol mapping and selector logic.
- Use local HTML fixtures in `testdata/` for deterministic browser tests.
- Keep browser integration tests explicit and separable from fast unit tests.
- Add regression tests for protocol edge cases before refactors.

## Design Priorities

- CDP foundation correctness before feature breadth.
- Accessibility snapshots are a first-class product surface, not a side utility.
- Cross-frame behavior must work from the start; do not adopt DOM node ID designs that collapse on iframes.
- Browser reuse and attach-to-existing-browser workflows are required, not optional.
