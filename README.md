# navigo

Pure Go browser automation for CDP-first agentic workflows.

Current state:

- Design is in [DESIGN.md](./DESIGN.md)
- Core CDP transport/session foundation is in place
- Browser connection, page creation, navigation, lifecycle waiting, and JS evaluation are implemented
- Early CDP code generation exists in `tools/cdpgen`

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
