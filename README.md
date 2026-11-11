# navigo

Pure Go browser automation for CDP-first agentic workflows.

Current state:

- Design is in [DESIGN.md](./DESIGN.md)
- Core CDP transport/session foundation is in place
- Browser connection, page creation, navigation, lifecycle waiting, JS evaluation, and remote object handles are implemented
- Early DOM selector, node-resolution, and element helper APIs are implemented
- Seed CDP code generation exists in `tools/cdpgen` for `Accessibility`, `Browser`, `CSS`, `DOM`, `DOMSnapshot`, `Emulation`, `Fetch`, `IO`, `Input`, `Log`, `Network`, `Overlay`, `Page`, `Performance`, `Runtime`, `Security`, `Storage`, and `Target`

## Low-level header dictionaries

Generated `network.Headers` and `fetch.Headers` use `map[string]json.RawMessage`
for CDP's open header objects. Members survive decoding and can be set explicitly:

```go
headers := network.Headers{"Accept": json.RawMessage(`"application/json"`)}
err := network.SetExtraHTTPHeaders(headers).Do(ctx, session)
```

A zero-value dictionary encodes as `{}`. These low-level types can contain
sensitive headers; the high-level `NetworkEvent` observer continues to omit headers.

## Development

```bash
just setup
just check
just refresh-cdp-protocol
just print-cdp-domains tools/cdpgen/testdata/browser_protocol.json,tools/cdpgen/testdata/js_protocol.json
just gen-cdp-seed
```

`tools/cdpgen/testdata/` carries a checked-in CDP protocol snapshot for deterministic generation and tests. Refresh it explicitly when you want to pick up newer protocol changes.

## Scope

- High-level Go SDK for browser automation
- Accessibility-first agent API with stable refs
- Built-in MCP server
- Future WebDriver BiDi backend
