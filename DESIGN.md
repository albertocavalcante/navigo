# Navigo — Browser Automation SDK for Go

> Pure Go browser automation library built for agentic AI and scraping workloads.
> Equal or better than Puppeteer/Playwright for Go developers.

---

## Table of Contents

1. [Research Summary](#research-summary)
2. [Protocol Deep Dive](#protocol-deep-dive)
3. [Architecture](#architecture)
4. [Core Design Decisions](#core-design-decisions)
5. [API Surface](#api-surface)
6. [MCP Server](#mcp-server)
7. [Stealth & Anti-Detection](#stealth--anti-detection)
8. [WebSocket Transport](#websocket-transport)
9. [Implementation Plan](#implementation-plan)
10. [References](#references)

---

## Research Summary

### Existing Go Landscape

| Library | Stars | Architecture | Cross-browser | AI-ready | Verdict |
|---------|-------|-------------|---------------|----------|---------|
| [chromedp][chromedp] | 13k | DOM node ID | Chrome only | No | Production-proven but architecturally limited — DOM node IDs break across frames, fixed event buffer causes deadlocks, no auto-wait |
| [rod][rod] | 6.8k | Remote object ID | Chrome only | No ([rod-mcp][rod-mcp] exists separately) | Better architecture than chromedp — auto-wait, thread-safe goob events, ships specific Chromium version, pluggable WebSocket |
| [playwright-go][playwright-go] | ~2k | Go wrapper → Node.js process → browser | Chromium+Firefox+WebKit | No | Not pure Go — spawns Node.js runtime (~50 MB overhead), community-maintained, incomplete coverage |
| [gowebdriver][gowebdriver] | <1k | WebDriver BiDi | Chrome+Firefox | No | Only Go BiDi implementation, dormant since mid-2024 |

### Browser Protocols

| Protocol | Status | Browsers | Future |
|----------|--------|----------|--------|
| [CDP][cdp-spec] (Chrome DevTools Protocol) | Mature, dominant | Chrome/Chromium/Edge | Continues in Chromium; not standardized; [100+ domains][cdp-domains] |
| [WebDriver BiDi][bidi-spec] | W3C standard in progress | Chrome, Firefox, Edge (Safari planned) | The cross-browser future — bidirectional, event-driven, [8 modules][bidi-spec] |
| Firefox CDP | [Deprecated][firefox-cdp-deprecated] | Firefox (disabled by default since v129) | Dead — Firefox is BiDi-only going forward |
| Safari/WebKit | WebKit Debug Protocol | Safari | Apple participates in BiDi working group; Playwright patches WebKit for CDP-like control |

### Industry Trends (2025-2026)

1. **Raw CDP over frameworks** — [browser-use][browser-use] (91k stars) and [Stagehand v3][stagehand] both dropped Playwright for raw CDP, reporting [5x][browser-use-cdp-post] and 44% performance improvements. The rationale: high-level frameworks obscure browser details that AI agents need.

2. **Accessibility tree as primary representation** — ~200-400 tokens vs 3,000-5,000 for full DOM. [Playwright MCP][playwright-mcp], [browser-use][browser-use], and [agent-browser][agent-browser] all use the accessibility tree as the primary page representation for LLM consumption.

3. **Stable element refs** — The `[ref=e1]`, `[ref=e2]` pattern from [Playwright MCP][playwright-mcp] gives LLMs deterministic, token-cheap handles for element interaction. [rod-mcp][rod-mcp] adopted the same pattern.

4. **Hybrid vision + accessibility** — Screenshots supplement the accessibility tree for canvas, charts, and images. Amazon Nova Act achieves 0.939 ScreenSpot score with this hybrid approach.

5. **Lightweight headless browsers** — [Lightpanda][lightpanda] (Zig-based, 11x faster, 9x less memory, CDP-compatible) emerging for AI agent workloads.

6. **MCP as the integration layer** — [Playwright MCP][playwright-mcp] (50+ tools), [rod-mcp][rod-mcp] (12 tools), [chrome-devtools-mcp][chrome-devtools-mcp] (34 tools) all exist. MCP is the standard way AI agents talk to browsers.

---

## Protocol Deep Dive

### CDP Connection Model

Chrome launched with `--remote-debugging-port=9222` exposes an HTTP server for browser discovery:

| Endpoint | Purpose | Returns |
|----------|---------|---------|
| `GET /json/version` | Browser metadata | `Browser`, `Protocol-Version`, `V8-Version`, **`webSocketDebuggerUrl`** (browser-level WS) |
| `GET /json/list` | Debuggable targets | Array of `{id, title, url, type, webSocketDebuggerUrl}` per target |
| `GET /json/new?{url}` | Create new page | Target descriptor for the new page |
| `GET /json/close/{id}` | Close target | `"Target is closing"` |
| `GET /json/activate/{id}` | Foreground target | `"Target activated"` |
| `GET /json/protocol` | Protocol definition | Full CDP JSON spec (gzipped) |

Target types: `page`, `iframe`, `background_page`, `service_worker`, `shared_worker`, `worker`, `webview`, `tab`, `other`.

### CDP Session Multiplexing

A single WebSocket connection to the browser-level URL can control multiple targets via **flattened sessions** ([`Target.attachToTarget`][cdp-target] with `flatten: true`):

```
1. Connect WS to browser URL (from /json/version → webSocketDebuggerUrl)
2. Target.setDiscoverTargets({discover: true}) → receive Target.targetCreated events
3. Target.attachToTarget({targetId: "...", flatten: true}) → get sessionId
4. All subsequent messages include sessionId field for routing
```

**Flattened message format** (no double JSON encoding):

```json
// Command to a specific session
{"id": 3, "method": "Page.navigate", "sessionId": "sess-uuid", "params": {"url": "https://example.com"}}

// Response from that session
{"id": 3, "sessionId": "sess-uuid", "result": {"frameId": "F1", "loaderId": "L1"}}

// Event from that session
{"method": "Page.loadEventFired", "sessionId": "sess-uuid", "params": {"timestamp": 1234567890.123}}

// Messages without sessionId belong to the root (browser) session
```

**Message routing in Go** — a single goroutine reads the WebSocket and dispatches:
- Has `sessionId` + `id` → response to a pending command in that session
- Has `sessionId` + `method` only → event for that session's listeners
- No `sessionId` + `id` → response to a browser-level command
- No `sessionId` + `method` → browser-level event

### CDP Domains We Need

| Domain | Priority | Purpose |
|--------|----------|---------|
| [Target][cdp-target] | P0 | Session multiplexing, tab management |
| [Page][cdp-page] | P0 | Navigation, lifecycle, screenshots, PDF, script injection |
| [Runtime][cdp-runtime] | P0 | JS evaluation, remote objects, console |
| [DOM][cdp-dom] | P0 | Node inspection, querySelector, attributes |
| [Input][cdp-input] | P0 | Mouse, keyboard, touch simulation |
| [Network][cdp-network] | P1 | Passive request/response monitoring |
| [Fetch][cdp-fetch] | P1 | Active request interception, mocking, modification |
| [Accessibility][cdp-accessibility] | P0 | AX tree extraction — the AI layer |
| [Emulation][cdp-emulation] | P2 | Viewport, user-agent, geolocation, touch, network throttle |
| [Browser][cdp-browser] | P1 | Version info, window management |
| [Storage][cdp-storage] | P2 | Cookies, localStorage, cache |
| [CSS][cdp-css] | P3 | Computed styles, media queries |
| [Log][cdp-log] | P3 | Browser console entries |

### CDP Code Generation

The CDP protocol spec is published in two forms:

- **PDL** (Protocol Definition Language) — indentation-based, lives in [Chromium source][chromium-pdl] at `third_party/blink/public/devtools_protocol/browser_protocol.pdl`
- **JSON** — machine-readable export, published as `browser_protocol.json` + `js_protocol.json`

**How predecessors generate types:**

| Library | Source | Strategy | Output |
|---------|--------|----------|--------|
| [chromedp/cdproto-gen][cdproto-gen] | JSON | One Go package per domain, command functions, event structs, enum constants | [cdproto][cdproto] — 60+ packages |
| [go-rod/rod][rod] | JSON | Single `proto` package, command structs with `Call(client)` methods | `lib/proto/` — flat |

**Our approach:** Generate from JSON spec (same as predecessors). One package per domain under `cdp/proto/`. Each command is a struct with `Do(session)` method. Each event is a struct. Each type maps to a Go struct. Enums become `type X string` with constants.

### Accessibility Tree via CDP

The [`Accessibility`][cdp-accessibility] domain exposes three key commands:

**`Accessibility.getFullAXTree`** — returns the complete accessibility tree:
```json
{"id": 1, "method": "Accessibility.getFullAXTree", "params": {"depth": -1, "frameId": "F1"}}
```

Response contains an array of `AXNode`:
```
AXNode {
  nodeId: string              // unique within the AX tree
  ignored: boolean            // filtered out for accessibility
  role: AXValue               // {type: "role", value: "button"}
  name: AXValue               // {type: "computedString", value: "Submit"}
  description: AXValue        // accessible description
  value: AXValue              // current value (for inputs)
  properties: AXProperty[]    // focusable, focused, disabled, checked, expanded, level, etc.
  parentId: string            // parent AXNodeId
  childIds: string[]          // children AXNodeIds
  backendDOMNodeId: integer   // maps back to DOM node for actions
  frameId: string             // which frame this belongs to
}
```

**`Accessibility.queryAXTree`** — search by accessible name or role:
```json
{"id": 2, "method": "Accessibility.queryAXTree", "params": {"accessibleName": "Submit", "role": "button"}}
```

**`Accessibility.enable`** — enables incremental events:
- `Accessibility.loadComplete` — full tree root after page load
- `Accessibility.nodesUpdated` — differential updates (changed AXNodes only)

**How [Playwright MCP][playwright-mcp] builds snapshots from this:**
1. Call `getFullAXTree` for the target frame
2. Filter out `ignored: true` nodes
3. Build tree from `parentId`/`childIds`
4. Extract role, name, value, checked/selected/expanded/disabled from properties
5. Assign sequential refs (`[ref=e1]`, `[ref=e2]`) to interactive elements
6. Serialize to indented text (~200-400 tokens per page)
7. Use `backendDOMNodeId` to map refs back to DOM nodes for click/type actions

### Input Simulation via CDP

The [`Input`][cdp-input] domain simulates human interaction at the OS level.

**Mouse click** requires three events (move → press → release):
```json
{"method": "Input.dispatchMouseEvent", "params": {"type": "mouseMoved", "x": 150, "y": 300}}
{"method": "Input.dispatchMouseEvent", "params": {"type": "mousePressed", "x": 150, "y": 300, "button": "left", "clickCount": 1, "buttons": 1}}
{"method": "Input.dispatchMouseEvent", "params": {"type": "mouseReleased", "x": 150, "y": 300, "button": "left", "clickCount": 1, "buttons": 0}}
```

**Realistic keystroke** requires three events per character (rawKeyDown → char → keyUp):
```json
{"method": "Input.dispatchKeyEvent", "params": {"type": "rawKeyDown", "key": "a", "code": "KeyA", "windowsVirtualKeyCode": 65}}
{"method": "Input.dispatchKeyEvent", "params": {"type": "char", "text": "a", "key": "a", "code": "KeyA"}}
{"method": "Input.dispatchKeyEvent", "params": {"type": "keyUp", "key": "a", "code": "KeyA", "windowsVirtualKeyCode": 65}}
```

**Bulk text** (non-realistic, fast): `Input.insertText({text: "Hello, World!"})`.

**Touch events**: `Input.dispatchTouchEvent` with types `touchStart`, `touchMove`, `touchEnd`, `touchCancel` and `touchPoints` array.

**Realistic input strategy** for stealth/anti-bot:
- Use `rawKeyDown`/`char`/`keyUp` per character (not `insertText`)
- Random delays between keystrokes (50-150ms, occasional longer pauses)
- Mouse movement along bezier curves before clicking (not instant teleport)
- Slight randomness in click coordinates (not pixel-perfect element center)
- Occasional corrections (wrong char → backspace → retype)

### Network Interception via CDP

Two domains serve different purposes:

| Aspect | [`Network`][cdp-network] domain | [`Fetch`][cdp-fetch] domain |
|--------|--------------------------------|----------------------------|
| Mode | **Passive** — observe only | **Active** — intercept, modify, block, mock |
| Enable | `Network.enable` | `Fetch.enable({patterns: [...]})` |
| Events | `requestWillBeSent`, `responseReceived`, `loadingFinished` | `requestPaused` (request/response stage) |
| Read body | `Network.getResponseBody` after `loadingFinished` | `Fetch.getResponseBody` while paused |
| Modify request | No | `Fetch.continueRequest({url, method, headers, postData})` |
| Block request | No | `Fetch.failRequest({errorReason: "BlockedByClient"})` |
| Mock response | No | `Fetch.fulfillRequest({responseCode, body, headers})` |
| Auth handling | No | `Fetch.continueWithAuth({username, password})` |

**Critical**: When using Fetch, `requestPaused` **blocks the request** until you respond with `continueRequest`, `failRequest`, or `fulfillRequest`. Failure to respond hangs the request forever. The library must always respond, even with a pass-through `continueRequest`.

`Network.setRequestInterception` is **deprecated** — use Fetch instead. Both domains can coexist: Network for observation, Fetch for interception.

### WebDriver BiDi Protocol

[W3C spec][bidi-spec] — the cross-browser future. Key differences from CDP:

| Aspect | CDP | WebDriver BiDi |
|--------|-----|-----------------|
| Message format | No `type` field | `type: "success"/"error"/"event"` |
| Session model | `sessionId` on messages | Built into browsingContext IDs |
| Event subscription | `Domain.enable()` enables all domain events | `session.subscribe({events: [...]})` — explicit, per-context |
| Standardized | No (Chrome-specific) | W3C standard |

**BiDi modules:**

| Module | Commands | Events |
|--------|----------|--------|
| `session` | `status`, `new`, `end`, `subscribe`, `unsubscribe` | — |
| `browser` | `close`, `createUserContext`, `getUserContexts`, etc. | — |
| `browsingContext` | `navigate`, `create`, `close`, `captureScreenshot`, `print`, `getTree`, `locateNodes`, `reload`, `setViewport`, etc. (13 total) | `contextCreated`, `load`, `domContentLoaded`, `navigationStarted`, etc. (14 total) |
| `network` | `addIntercept`, `continueRequest`, `continueResponse`, `failRequest`, `provideResponse`, etc. (13 total) | `beforeRequestSent`, `responseStarted`, `responseCompleted`, `authRequired`, `fetchError` |
| `script` | `evaluate`, `callFunction`, `addPreloadScript`, `getRealms`, etc. (6 total) | `message`, `realmCreated`, `realmDestroyed` |
| `input` | `performActions`, `releaseActions`, `setFiles` | `fileDialogOpened` |
| `storage` | `getCookies`, `setCookie`, `deleteCookies` | — |
| `log` | — | `entryAdded` |
| `emulation` | `setGeolocationOverride`, `setUserAgentOverride`, `setNetworkConditions`, etc. (11 total) | — |

**BiDi message format:**
```json
// Command
{"id": 42, "method": "browsingContext.navigate", "params": {"context": "ctx-1", "url": "https://example.com", "wait": "complete"}}

// Success response
{"type": "success", "id": 42, "result": {"navigation": "nav-1", "url": "https://example.com"}}

// Error response
{"type": "error", "id": 42, "error": "no such frame", "message": "Browsing context not found"}

// Event
{"type": "event", "method": "network.beforeRequestSent", "params": {"context": "ctx-1", "request": {...}}}
```

---

## Architecture

### Project Layout

```
navigo/
├── cdp/                        # Low-level CDP client
│   ├── conn.go                 # WebSocket connection, message routing
│   ├── session.go              # CDP session (per-target command/event dispatch)
│   ├── transport.go            # Transport interface (WebSocket, pipe)
│   └── proto/                  # Auto-generated CDP domain types + commands
│       ├── target/             # Target domain (attachToTarget, etc.)
│       ├── page/               # Page domain (navigate, screenshot, PDF)
│       ├── dom/                # DOM domain (querySelector, attributes)
│       ├── runtime/            # Runtime domain (evaluate, remote objects)
│       ├── network/            # Network domain (passive monitoring)
│       ├── fetch/              # Fetch domain (active interception)
│       ├── input/              # Input domain (mouse, keyboard, touch)
│       ├── accessibility/      # Accessibility domain (AX tree)
│       ├── emulation/          # Emulation domain (viewport, UA, geo)
│       ├── browser/            # Browser domain (version, windows)
│       ├── storage/            # Storage domain (cookies, cache)
│       └── ...                 # All other CDP domains
│
├── bidi/                       # WebDriver BiDi client (Phase 7)
│   ├── conn.go                 # BiDi WebSocket connection
│   ├── session.go              # BiDi session with subscribe model
│   └── proto/                  # BiDi protocol types (generated or hand-written)
│
├── browser.go                  # Browser lifecycle (Launch, Connect, Close)
├── context.go                  # Browser context (isolation, cookies, storage)
├── page.go                     # Page operations (Navigate, Screenshot, PDF, Evaluate)
├── element.go                  # Element handle (Click, Type, Text, Attribute)
├── frame.go                    # Frame/iframe handling (cross-frame operations)
├── input.go                    # Keyboard + mouse simulation (realistic mode)
├── network.go                  # Network interception (OnRequest, OnResponse)
├── wait.go                     # Auto-wait strategies (Visible, Attached, Stable, Enabled)
├── selector.go                 # CSS + XPath + text + role selectors
├── options.go                  # Functional options (LaunchOption, ContextOption, etc.)
├── errors.go                   # Typed errors (TimeoutError, NavigationError, etc.)
│
├── ax/                         # Accessibility tree — the AI/agent layer
│   ├── tree.go                 # Full AX tree from CDP, parent/child linking
│   ├── snapshot.go             # Token-optimized page snapshot with stable refs
│   ├── ref.go                  # Stable element references (ref=e1, ref=e2)
│   ├── serialize.go            # Markdown serialization for LLM consumption
│   ├── filter.go               # Tree pruning (remove ignored, decorative, redundant)
│   └── extract.go              # Structured data extraction with Go generics
│
├── stealth/                    # Anti-detection evasions
│   ├── stealth.go              # Apply all evasions via Page.addScriptToEvaluateOnNewDocument
│   ├── webdriver.go            # navigator.webdriver = undefined
│   ├── chrome_runtime.go       # window.chrome.runtime object
│   ├── plugins.go              # navigator.plugins / navigator.mimeTypes
│   ├── webgl.go                # WebGL vendor/renderer spoofing
│   ├── permissions.go          # Permissions API consistency
│   ├── languages.go            # navigator.languages
│   ├── dimensions.go           # window.outerWidth/outerHeight
│   ├── iframe.go               # iframe.contentWindow.chrome consistency
│   ├── codec.go                # Media codec support consistency
│   ├── fingerprint.go          # Canvas/AudioContext fingerprint noise
│   └── headless.go             # Headless detection evasion
│
├── launcher/                   # Browser binary management
│   ├── launcher.go             # Find system Chrome, launch with flags, connect
│   ├── download.go             # Download specific Chrome version (like rod)
│   ├── flags.go                # Chrome CLI flags builder
│   └── remote.go               # Connect to existing browser via debug port
│
├── pool/                       # Connection pooling for concurrent scraping
│   ├── pool.go                 # Browser context pool with acquire/release
│   └── limiter.go              # Rate limiting, concurrency control
│
├── mcp/                        # MCP server implementation
│   ├── server.go               # MCP stdio server (JSON-RPC 2.0)
│   ├── tools.go                # Tool definitions and handlers
│   ├── tools_navigate.go       # Navigation tools
│   ├── tools_interact.go       # Interaction tools (click, type, select)
│   ├── tools_snapshot.go       # Snapshot and screenshot tools
│   ├── tools_network.go        # Network inspection tools
│   ├── tools_storage.go        # Cookie/storage tools
│   ├── tools_emulation.go      # Viewport/UA/geo tools
│   └── tools_wait.go           # Wait tools
│
├── cmd/
│   └── navigo-mcp/             # MCP server binary
│       └── main.go
│
├── internal/
│   └── ws/                     # Minimal WebSocket client (RFC 6455)
│       ├── conn.go             # Dial, Read, Write, Close
│       ├── frame.go            # Frame encoding/decoding (text + close + pong)
│       └── handshake.go        # HTTP Upgrade handshake
│
├── examples/
│   ├── screenshot/             # Take a screenshot
│   ├── scrape/                 # Extract structured data
│   ├── intercept/              # Network interception
│   ├── agent/                  # AI agent with accessibility tree
│   ├── stealth/                # Stealth mode scraping
│   └── pool/                   # Concurrent scraping with pool
│
├── testdata/                   # Test fixtures, HTML pages for tests
└── tools/
    └── cdpgen/                 # CDP protocol code generator (standalone)
        ├── main.go
        └── browser_protocol.json
```

### Dependency Graph

```
cmd/navigo-mcp
  └── mcp/
       └── navigo (top-level: browser, page, element, etc.)
            ├── ax/
            ├── stealth/
            ├── launcher/
            ├── pool/
            └── cdp/
                 ├── cdp/proto/* (generated)
                 └── internal/ws/
```

Most packages exist to support the module internally. The intended public API surface is:
- `navigo` — the high-level library (Browser, Page, Element, etc.)
- `navigo/ax` — accessibility tree / agent layer
- `navigo/cdp` — low-level CDP client (for advanced users)
- `navigo/mcp` — MCP server (for embedding)
- `navigo/stealth` — anti-detection (for selective use)
- `navigo/launcher` — browser management
- `navigo/pool` — connection pooling

---

## Core Design Decisions

### 1. Remote Object ID Architecture

chromedp uses DOM node IDs (`DOM.NodeId`) which are **integers that get invalidated** when the DOM changes and **do not work across frames**. This is a fundamental architectural limitation that has plagued chromedp for years ([iframe JS evaluation was open for 3+ years][chromedp-iframe]).

We use **remote object IDs** (`Runtime.RemoteObjectId`) — opaque strings that reference JavaScript objects in the V8 heap. These are:
- Stable within an execution context
- Work across frames (each frame has its own execution context)
- The same architecture used by Rod and Puppeteer

They are **not** durable across navigation, execution-context destruction, page close, or explicit object release. Public APIs must therefore define stale-handle behavior:
- Selector-created elements should retain enough metadata to re-resolve when appropriate
- AX ref interactions should re-resolve through `backendDOMNodeId` or fresh queries
- Operations on invalid handles should return a typed `StaleElementError`

```go
type Element struct {
    objectID  string  // Runtime.RemoteObjectId
    frame     *Frame
    page      *Page
    session   *cdp.Session
}
```

### 2. Auto-Wait Built In

Every element interaction auto-waits for the element to be:
- **Attached** to the DOM (`DOM.describeNode` succeeds)
- **Visible** (not `display:none`, not zero-size, not `visibility:hidden`)
- **Stable** (bounding box unchanged across two animation frames)
- **Enabled** (not `disabled` attribute)
- **Not obscured** (element at click point is the target or a descendant)

```go
// Auto-waits internally — no manual sleep/poll needed
err := page.Click("#submit")

// Explicit wait with options when needed
el, err := page.WaitFor("#result", navigo.Visible, navigo.Timeout(5*time.Second))
```

**Implementation**: Poll via `Runtime.evaluate` checking computed style + bounding box, with exponential backoff from 50ms to 500ms intervals, bounded by a configurable timeout (default 30s).

### 3. Accessibility Tree as First-Class Citizen

The `ax` package provides AI-optimized page representation. This is our primary differentiator.

```go
// Get token-optimized page snapshot
snap, err := page.Snapshot()
fmt.Println(snap.Markdown())
// Output (~200-400 tokens):
// - navigation "Main Menu"
//   - link "Home" [ref=e1]
//   - link "Products" [ref=e2]
// - main
//   - heading "Welcome" [level=1]
//   - textbox "Search..." [ref=e3]
//   - button "Search" [ref=e4]

// Interact via stable refs (resolves ref → backendDOMNodeId → click coordinates)
err = snap.Click("e4")
err = snap.Type("e3", "golang browser")

// Structured extraction with generics
type Product struct {
    Name  string  `ax:"heading"`
    Price float64 `ax:"text,pattern=\\$([\\d.]+)"`
    URL   string  `ax:"link,attr=href"`
}
products, err := ax.Extract[Product](page, ax.Scope("article.product"))
```

**Snapshot internals:**
1. Call `Accessibility.getFullAXTree(depth: -1)` for all frames
2. Filter ignored nodes, decorative elements, empty text nodes
3. Stitch cross-frame trees (using `frameId` on each AXNode)
4. Assign sequential refs (`e1`, `e2`, ...) to interactive elements (links, buttons, inputs, selects, checkboxes, etc.)
5. Build ref→backendDOMNodeId mapping for action dispatch
6. Serialize to indented markdown with role, name, value, and state properties

**Incremental snapshots** (optimization): After `Accessibility.enable`, listen for `nodesUpdated` events and patch the tree rather than re-fetching. Expose `snap.Refresh()` for explicit re-fetch.

### 4. Network Layer

```go
// Passive monitoring (Network domain)
page.OnResponse(func(resp *navigo.Response) {
    if resp.URL.Path == "/api/data" {
        body, _ := resp.Body() // calls Network.getResponseBody
        // process API response
    }
})

// Active interception (Fetch domain)
page.OnRequest(func(req *navigo.InterceptedRequest) {
    if req.ResourceType == "image" {
        req.Abort() // Fetch.failRequest
        return
    }
    if req.URL.Host == "api.example.com" {
        req.SetHeader("Authorization", "Bearer token")
    }
    req.Continue() // Fetch.continueRequest — MUST be called
})

// Mock responses
page.Route("*/api/user", func(req *navigo.InterceptedRequest) {
    req.Fulfill(200, map[string]string{"Content-Type": "application/json"},
        []byte(`{"name": "test"}`))
})
```

### 5. Stealth Mode

All evasions are applied via [`Page.addScriptToEvaluateOnNewDocument`][cdp-page] — the script runs before any page JavaScript, in every new document including iframes.

```go
// All evasions applied automatically
browser, err := navigo.Launch(navigo.Stealth())

// Or connect to existing browser (preserves cookies/sessions)
browser, err := navigo.Connect("http://localhost:9222", navigo.Stealth())
```

### 6. Concurrent Session Pool

```go
pool, err := navigo.NewPool(
    navigo.PoolSize(10),           // 10 browser contexts
    navigo.PoolStealth(),          // stealth on all contexts
    navigo.PoolRateLimit(2, time.Second), // max 2 pages/sec
)
defer pool.Close()

// Acquire a context, execute, release
err = pool.Do(ctx, func(page *navigo.Page) error {
    if err := page.Navigate("https://example.com"); err != nil {
        return err
    }
    snap, err := page.Snapshot()
    if err != nil {
        return err
    }
    // extract data from snap...
    return nil
})
```

### 7. Error Model

Typed errors for actionable error handling:

```go
var timeoutErr *navigo.TimeoutError
if errors.As(err, &timeoutErr) {
    // element not found within timeout
}

var navErr *navigo.NavigationError
if errors.As(err, &navErr) {
    // navigation failed (net::ERR_*, HTTP error, etc.)
    fmt.Println(navErr.StatusCode, navErr.NetError)
}

var sessionErr *navigo.SessionClosedError
if errors.As(err, &sessionErr) {
    // target/page was closed or crashed
}
```

---

## API Surface

### Browser Lifecycle

```go
package navigo

// Launch a new Chrome/Chromium instance
func Launch(opts ...LaunchOption) (*Browser, error)

// Connect to an existing browser via CDP
func Connect(debugURL string, opts ...ConnectOption) (*Browser, error)

// LaunchOption
func Headless() LaunchOption                    // --headless=new (Chrome 112+)
func Stealth() LaunchOption                     // apply all anti-detection evasions
func ChromePath(path string) LaunchOption       // explicit binary path
func UserDataDir(dir string) LaunchOption       // persistent profile
func ProxyServer(url string) LaunchOption       // --proxy-server=url
func ChromeFlags(flags ...string) LaunchOption  // arbitrary flags
func NoDefaultArgs() LaunchOption               // skip default flags

type Browser struct { /* unexported */ }
func (b *Browser) NewContext(opts ...ContextOption) (*Context, error)
func (b *Browser) DefaultContext() *Context
func (b *Browser) Contexts() []*Context
func (b *Browser) Version() (string, error)
func (b *Browser) Close() error

// ContextOption
func IgnoreHTTPSErrors() ContextOption
func SetViewport(width, height int) ContextOption
func SetUserAgent(ua string) ContextOption
func SetGeolocation(lat, lon float64) ContextOption

type Context struct { /* unexported */ }
func (c *Context) NewPage() (*Page, error)
func (c *Context) Pages() []*Page
func (c *Context) Cookies(urls ...string) ([]*Cookie, error)
func (c *Context) SetCookies(cookies ...*Cookie) error
func (c *Context) ClearCookies() error
func (c *Context) Close() error
```

### Page

```go
type Page struct { /* unexported */ }

// Navigation
func (p *Page) Navigate(url string) error
func (p *Page) Reload() error
func (p *Page) GoBack() error
func (p *Page) GoForward() error
func (p *Page) URL() string
func (p *Page) Title() (string, error)
func (p *Page) Content() (string, error)
func (p *Page) Close() error

// Element selection (auto-waits)
func (p *Page) Query(selector string) (*Element, error)
func (p *Page) QueryAll(selector string) ([]*Element, error)

// Convenience (auto-waits for element, then acts)
func (p *Page) Click(selector string, opts ...ClickOption) error
func (p *Page) Type(selector string, text string, opts ...TypeOption) error
func (p *Page) Select(selector string, values ...string) error
func (p *Page) Check(selector string) error
func (p *Page) Uncheck(selector string) error
func (p *Page) Focus(selector string) error
func (p *Page) Hover(selector string) error

// JavaScript
func (p *Page) Evaluate(expr string, args ...any) (any, error)
func (p *Page) EvaluateHandle(expr string, args ...any) (*Element, error)

// Capture
func (p *Page) Screenshot(opts ...ScreenshotOption) ([]byte, error)
func (p *Page) PDF(opts ...PDFOption) ([]byte, error)

// Accessibility — the agent API
func (p *Page) Snapshot(opts ...SnapshotOption) (*ax.Snapshot, error)

// Waiting
func (p *Page) WaitFor(selector string, opts ...WaitOption) (*Element, error)
func (p *Page) WaitForNavigation(opts ...WaitOption) error
func (p *Page) WaitForNetworkIdle(opts ...WaitOption) error
func (p *Page) WaitForSelector(selector string, opts ...WaitOption) (*Element, error)

// Network
func (p *Page) OnRequest(fn func(*Request)) func()       // returns unsubscribe
func (p *Page) OnResponse(fn func(*Response)) func()     // returns unsubscribe
func (p *Page) Route(pattern string, fn func(*InterceptedRequest)) func()

// Frames
func (p *Page) MainFrame() *Frame
func (p *Page) Frames() []*Frame

// Input
func (p *Page) Keyboard() *Keyboard
func (p *Page) Mouse() *Mouse

// Events
func (p *Page) OnDialog(fn func(*Dialog)) func()
func (p *Page) OnConsole(fn func(*ConsoleMessage)) func()
func (p *Page) OnPageError(fn func(error)) func()
```

### Element

```go
type Element struct { /* unexported */ }

func (e *Element) Click(opts ...ClickOption) error
func (e *Element) Type(text string, opts ...TypeOption) error
func (e *Element) Press(key string) error
func (e *Element) SelectOption(values ...string) error
func (e *Element) Check() error
func (e *Element) Uncheck() error
func (e *Element) Focus() error
func (e *Element) Hover() error
func (e *Element) ScrollIntoView() error
func (e *Element) Tap() error

func (e *Element) Text() (string, error)
func (e *Element) InnerHTML() (string, error)
func (e *Element) InnerText() (string, error)
func (e *Element) Attribute(name string) (string, bool, error)
func (e *Element) Property(name string) (any, error)
func (e *Element) BoundingBox() (*Rect, error)
func (e *Element) Screenshot(opts ...ScreenshotOption) ([]byte, error)

func (e *Element) IsVisible() (bool, error)
func (e *Element) IsEnabled() (bool, error)
func (e *Element) IsChecked() (bool, error)

func (e *Element) Query(selector string) (*Element, error)
func (e *Element) QueryAll(selector string) ([]*Element, error)

func (e *Element) Frame() (*Frame, error)  // for iframe elements
```

### Accessibility / Agent API

```go
package ax

type Snapshot struct { /* unexported */ }

// Serialization
func (s *Snapshot) Markdown() string                     // LLM-optimized text representation
func (s *Snapshot) Nodes() []*Node                       // raw AX nodes
func (s *Snapshot) InteractiveNodes() []*Node            // only elements with refs

// Ref-based interaction
func (s *Snapshot) Click(ref string) error               // resolve ref → coordinates → click
func (s *Snapshot) Type(ref string, text string) error   // resolve ref → focus → type
func (s *Snapshot) Select(ref string, values ...string) error

// Lookup
func (s *Snapshot) FindByRef(ref string) (*Node, bool)
func (s *Snapshot) FindByRole(role string) []*Node
func (s *Snapshot) FindByName(name string) []*Node

// Refresh
func (s *Snapshot) Refresh() error                       // re-fetch from browser

// Structured extraction
func Extract[T any](page *Page, opts ...ExtractOption) ([]T, error)

type Node struct {
    Ref         string            // "e1", "e2", ... (empty for non-interactive)
    Role        string            // "button", "link", "textbox", "heading", etc.
    Name        string            // accessible name
    Value       string            // current value
    Description string            // accessible description
    Level       int               // heading level
    Checked     *bool             // tri-state
    Selected    bool
    Expanded    *bool             // tri-state
    Disabled    bool
    Focused     bool
    Required    bool
    Children    []*Node
}
```

---

## MCP Server

### Protocol

The MCP server uses [JSON-RPC 2.0][jsonrpc] over newline-delimited stdio, following the [Model Context Protocol][mcp-spec] spec.

**Lifecycle:**
1. Client sends `initialize` → server responds with capabilities
2. Client sends `notifications/initialized`
3. Normal tool calls via `tools/list` and `tools/call`
4. Shutdown: client closes stdin → SIGTERM → SIGKILL

**Go MCP framework:** Use [`mark3labs/mcp-go`][mcp-go] (1,880+ importers, rod-mcp uses it) or the [official Go SDK][mcp-go-sdk] (`modelcontextprotocol/go-sdk`).

### Tool Catalog

Designed after studying [Playwright MCP][playwright-mcp] (50+ tools), [rod-mcp][rod-mcp] (12 tools), and [chrome-devtools-mcp][chrome-devtools-mcp] (34 tools). We target a practical middle ground.

#### Navigation (7 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `navigate` | `url` (string, required) | Navigate to URL, wait for load |
| `go_back` | — | Browser history back |
| `go_forward` | — | Browser history forward |
| `reload` | — | Reload current page |
| `list_tabs` | — | List all open tabs with index, title, URL |
| `new_tab` | `url` (string, optional) | Open new tab |
| `select_tab` | `index` (int, required) | Switch to tab by index |
| `close_tab` | `index` (int, optional) | Close tab (current if omitted) |

#### Snapshot & Capture (4 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `snapshot` | — | Accessibility tree snapshot with stable refs |
| `screenshot` | `fullPage` (bool), `selector` (string), `width`/`height` (int) | Take screenshot, returns base64 PNG |
| `pdf` | `landscape` (bool), `format` (string) | Generate PDF, returns base64 |
| `console_messages` | `level` (string), `limit` (int) | Recent console log entries |

#### Interaction (9 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `click` | `ref` (string, required), `button` (string), `doubleClick` (bool) | Click element by ref |
| `type` | `ref` (string, required), `text` (string, required), `submit` (bool) | Type into element |
| `select_option` | `ref` (string, required), `values` (string[], required) | Select dropdown option(s) |
| `hover` | `ref` (string, required) | Hover over element |
| `press_key` | `key` (string, required) | Press keyboard key |
| `scroll` | `direction` (string, required), `amount` (int) | Scroll page or element |
| `drag` | `startRef` (string, required), `endRef` (string, required) | Drag from one element to another |
| `upload` | `ref` (string, required), `paths` (string[], required) | Upload file(s) to input |
| `handle_dialog` | `accept` (bool, required), `text` (string) | Accept or dismiss dialog |

#### Extraction (3 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `evaluate` | `expression` (string, required) | Execute JavaScript, return result |
| `query_text` | `selector` (string, required) | Get text content of element(s) |
| `query_attribute` | `selector` (string, required), `attribute` (string, required) | Get attribute value(s) |

#### Network (4 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `list_requests` | `filter` (string), `limit` (int) | List captured network requests |
| `get_request` | `index` (int, required), `part` (string: "headers"/"body"/"all") | Get request/response details |
| `intercept` | `pattern` (string, required), `action` (string: "block"/"modify"/"mock"), `response` (object) | Set up request interception |
| `clear_intercepts` | — | Remove all interception rules |

#### Storage (4 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `get_cookies` | `url` (string) | List cookies |
| `set_cookie` | `name`, `value`, `domain`, `path`, `httpOnly`, `secure`, `sameSite`, `expires` | Set a cookie |
| `delete_cookies` | `name` (string), `domain` (string) | Delete matching cookies |
| `clear_cookies` | — | Delete all cookies |

#### Wait (3 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `wait_for` | `ref` (string) OR `text` (string), `timeout` (int) | Wait for element/text to appear |
| `wait_for_navigation` | `timeout` (int) | Wait for navigation to complete |
| `wait_for_idle` | `timeout` (int) | Wait for network idle |

#### Emulation (3 tools)

| Tool | Parameters | Description |
|------|-----------|-------------|
| `set_viewport` | `width` (int, required), `height` (int, required), `deviceScaleFactor` (float) | Set viewport size |
| `set_user_agent` | `userAgent` (string, required) | Override user agent |
| `set_geolocation` | `latitude` (float, required), `longitude` (float, required) | Override geolocation |

**Total: 37 tools** across 7 categories.

### Usage

```json
{
  "mcpServers": {
    "navigo": {
      "command": "navigo-mcp",
      "args": ["--cdp", "http://localhost:9222"]
    }
  }
}
```

Or launch a fresh browser:

```json
{
  "mcpServers": {
    "navigo": {
      "command": "navigo-mcp",
      "args": ["--headless", "--stealth"]
    }
  }
}
```

---

## Stealth & Anti-Detection

All evasions are injected via [`Page.addScriptToEvaluateOnNewDocument`][cdp-page] — runs before any page JS, in every new document including iframes.

| # | Evasion | What It Defeats | Technique |
|---|---------|----------------|-----------|
| 1 | **navigator.webdriver** | `navigator.webdriver === true` check | `Object.defineProperty(navigator, 'webdriver', {get: () => undefined})` |
| 2 | **chrome.runtime** | Missing/inconsistent `window.chrome` in headless | Inject full `window.chrome.runtime` object with all expected nested objects and methods |
| 3 | **navigator.plugins** | Empty plugin array in headless | Inject 3 plugins (Chrome PDF Plugin, Chrome PDF Viewer, Native Client) with proper PluginArray prototype |
| 4 | **navigator.mimeTypes** | Empty mimeTypes in headless | Inject matching mimeTypes for the fake plugins |
| 5 | **navigator.languages** | Missing or `[]` | `Object.defineProperty(navigator, 'languages', {get: () => ['en-US', 'en']})` |
| 6 | **WebGL renderer** | `WEBGL_debug_renderer_info` returns "Google SwiftShader" in headless | Proxy `getParameter()` to return "Intel Inc." / "Intel Iris OpenGL Engine" for UNMASKED_VENDOR/RENDERER |
| 7 | **Permissions API** | `Notification.permission` vs `navigator.permissions.query` inconsistency | Override `permissions.query` for `notifications` to return `Notification.permission` |
| 8 | **window dimensions** | `outerWidth === 0 && outerHeight === 0` in headless | Return `innerWidth` / `innerHeight + 85` |
| 9 | **iframe chrome** | `iframe.contentWindow.chrome` should match parent | Proxy `HTMLIFrameElement.contentWindow` getter to inject chrome object |
| 10 | **Codec support** | Different `MediaSource.isTypeSupported()` results in headless | Override to match headed Chrome responses |
| 11 | **Canvas fingerprint** | Deterministic canvas output in automated browsers | Add subtle noise to `toDataURL()` and `toBlob()` output |
| 12 | **AudioContext** | Deterministic audio fingerprint | Add noise to `AnalyserNode.getFloatFrequencyData()` |
| 13 | **User-Agent** | "HeadlessChrome" in UA string | Launch with `--user-agent=<real UA>`, override `navigator.userAgent` |

**Reference:** Based on [puppeteer-extra-plugin-stealth][puppeteer-stealth] evasion set.

---

## WebSocket Transport

### Decision: Minimal Internal Implementation + Pluggable Interface

CDP WebSocket usage is simple: text frames only, no compression, no fragmentation (Chrome doesn't support receiving fragmented frames — [Chromium issue 40125913][chromium-ws-fragment]). Rod proves a minimal client is ~180 lines.

**What we implement in `internal/ws/`:**
- RFC 6455 HTTP Upgrade handshake
- Text frame encoding (opcode 0x1, client-side masking with random 4-byte key)
- Text frame decoding (unmasked server frames)
- Close frame handling (opcode 0x8)
- Pong response to pings (opcode 0xA → 0x9)
- No compression (`permessage-deflate` not needed — Chrome CDP doesn't negotiate it)
- No fragmentation (send single frames; Chrome requires this)

**Why not an external library:**

| Library | Issue |
|---------|-------|
| [`golang.org/x/net/websocket`][x-net-ws] | [Self-deprecating][x-net-ws], no context support, no concurrent writes |
| [`gorilla/websocket`][gorilla-ws] | Mature but no native `context.Context` integration, maintenance mode |
| [`coder/websocket`][coder-ws] | Best API, zero deps, but adds an external dependency |
| [`gobwas/ws`][gobwas-ws] | Used by chromedp; zero-alloc but two transitive deps |

**Transport is pluggable** (same pattern as Rod). It should be cancellation-aware so higher layers can enforce timeouts and shutdown cleanly. Advanced users can inject their preferred WebSocket library:

```go
// Transport interface — implement for custom WebSocket
type Transport interface {
    Send(ctx context.Context, data []byte) error
    Read(ctx context.Context) ([]byte, error)
    Close() error
}

// Default uses our internal/ws implementation
browser, err := navigo.Connect("http://localhost:9222")

// Custom transport
browser, err := navigo.Connect("http://localhost:9222",
    navigo.WithTransport(myGorillaTransport))
```

### Message Size Handling

Chrome's CDP limits: 100 MB receive (client→browser), 256 MB send (browser→client).

| Operation | Typical Size | Max |
|-----------|-------------|-----|
| Normal command/response | 100 B – 10 KB | — |
| `DOM.getDocument` (deep) | 10 KB – 1 MB | ~10 MB |
| `Page.captureScreenshot` (base64, 1080p) | 500 KB – 2 MB | ~10 MB |
| `Page.captureScreenshot` (base64, 4K) | 2 MB – 8 MB | ~20 MB |
| `Page.printToPDF` (base64) | 1 MB – 50 MB | ~100 MB |
| `Accessibility.getFullAXTree` | 10 KB – 500 KB | ~5 MB |

Our read buffer grows dynamically per frame (allocate from frame header's declared length). No artificial read limit. Use `sync.Pool` for buffer reuse on large operations.

---

## Implementation Plan

### Phase 1: CDP Foundation (weeks 1-3)

- [ ] `internal/ws` — minimal WebSocket client (handshake, text frames, close, pong)
- [ ] `cdp/conn.go` — single WS connection, read goroutine, message routing by sessionId
- [ ] `cdp/session.go` — per-target session: send command (id-tracked), receive response, subscribe to events
- [ ] `tools/cdpgen` — code generator: parse `browser_protocol.json` + `js_protocol.json`, emit Go packages
- [ ] `cdp/proto/*` — generate all CDP domain types (Target, Page, DOM, Runtime, Input, Network, Fetch, Accessibility, Emulation, Browser, Storage, etc.)
- [ ] `launcher/` — find system Chrome, launch with `--remote-debugging-port`, parse `/json/version` + `/json/list`, connect
- [ ] `launcher/remote.go` — connect to existing browser via debug URL
- [ ] Integration test: launch Chrome, connect, send `Browser.getVersion`, verify

### Phase 2: Core Page API (weeks 3-5)

- [ ] `browser.go` — Launch/Connect, NewContext, Close
- [ ] `context.go` — browser context via `Target.createBrowserContext`, cookie isolation
- [ ] `page.go` — NewPage via `Target.createTarget`, Navigate (+ frame/loader tracking and explicit wait-until semantics, not just `Page.loadEventFired`), URL, Title, Content, Close
- [ ] `element.go` — Element backed by RemoteObjectId, Query/QueryAll via `Runtime.evaluate` + `document.querySelector`
- [ ] `wait.go` — auto-wait: poll visibility/stability/enabled with exponential backoff
- [ ] `selector.go` — CSS selectors, XPath (via `document.evaluate`), text selectors (walk tree)
- [ ] `input.go` — Mouse (moveTo bezier, click, doubleclick, drag) and Keyboard (rawKeyDown/char/keyUp per character)
- [ ] `frame.go` — Frame tree from `Page.frameTree`, cross-frame element access via session per frame
- [ ] Page.Evaluate — `Runtime.evaluate` with argument serialization and return value deserialization
- [ ] Page.Screenshot — `Page.captureScreenshot` (viewport, full page, element via clip)
- [ ] Page.PDF — `Page.printToPDF`
- [ ] Integration tests for all of the above

### Phase 3: Network & Events (weeks 5-6)

- [ ] `network.go` — passive monitoring: `Network.enable`, OnRequest/OnResponse event dispatch, `Network.getResponseBody`
- [ ] Request interception: `Fetch.enable` with patterns, OnRequest handler with Continue/Abort/Fulfill, `Route()` helper
- [ ] `context.go` — cookie management: `Storage.getCookies`, `Storage.setCookies`, `Storage.clearCookies`
- [ ] Page events: OnDialog (`Page.javascriptDialogOpening`), OnConsole (`Runtime.consoleAPICalled`), OnPageError (`Runtime.exceptionThrown`)
- [ ] File upload: `DOM.setFileInputFiles`
- [ ] File download: `Page.setDownloadBehavior` + `Browser.downloadProgress`
- [ ] Integration tests

### Phase 4: Accessibility & Agent Layer (weeks 6-8)

- [ ] `ax/tree.go` — `Accessibility.getFullAXTree`, parse AXNode array into tree, link parent/child
- [ ] `ax/filter.go` — prune ignored nodes, decorative elements, empty text, redundant containers
- [ ] `ax/ref.go` — assign sequential refs to interactive elements, build ref→backendDOMNodeId map
- [ ] `ax/serialize.go` — markdown serialization with role, name, value, states, indentation
- [ ] `ax/snapshot.go` — Snapshot type with Markdown(), Click(ref), Type(ref, text), FindByRef, FindByRole, Refresh
- [ ] `ax/extract.go` — `Extract[T]` with struct tags (`ax:"heading"`, `ax:"link,attr=href"`, `ax:"text,pattern=..."`)
- [ ] Cross-frame snapshot stitching (merge AX trees from all frames)
- [ ] Incremental updates via `Accessibility.enable` + `nodesUpdated` events
- [ ] Integration tests + benchmarks (token count per page)

### Phase 5: Stealth & Scraping (weeks 8-9)

- [ ] `stealth/` — all 13 evasion scripts, each in its own file
- [ ] `stealth/stealth.go` — `Apply(session)` injects all scripts via `Page.addScriptToEvaluateOnNewDocument`
- [ ] Integration with LaunchOption: `navigo.Stealth()` auto-applies
- [ ] User-agent rotation from realistic UA database
- [ ] `launcher/flags.go` — stealth-optimized Chrome flags (disable automation info bars, disable blink features, etc.)
- [ ] `pool/pool.go` — browser context pool: acquire/release, configurable size
- [ ] `pool/limiter.go` — rate limiter: requests per second, concurrent pages, backoff on errors
- [ ] Anti-detection test suite (test against known fingerprint checkers)

### Phase 6: MCP Server (weeks 9-10)

- [ ] `mcp/server.go` — MCP stdio server using [mcp-go][mcp-go] or [official SDK][mcp-go-sdk]
- [ ] `mcp/tools_navigate.go` — 7 navigation tools
- [ ] `mcp/tools_snapshot.go` — 4 snapshot/capture tools
- [ ] `mcp/tools_interact.go` — 9 interaction tools
- [ ] `mcp/tools_extract.go` — 3 extraction tools
- [ ] `mcp/tools_network.go` — 4 network tools
- [ ] `mcp/tools_storage.go` — 4 storage tools
- [ ] `mcp/tools_wait.go` — 3 wait tools
- [ ] `mcp/tools_emulation.go` — 3 emulation tools
- [ ] `cmd/navigo-mcp/main.go` — CLI binary with `--cdp`, `--headless`, `--stealth` flags
- [ ] MCP integration tests (simulate tool calls, verify responses)

### Phase 7: WebDriver BiDi & Cross-Browser (future)

- [ ] `bidi/conn.go` — BiDi WebSocket connection (same transport, different message format)
- [ ] `bidi/session.go` — BiDi session with `session.subscribe` model
- [ ] `bidi/proto/` — BiDi protocol types (8 modules, hand-written or generated from spec)
- [ ] Firefox support via BiDi
- [ ] Unified high-level API: `browser.go` dispatches to CDP or BiDi backend based on browser type
- [ ] Safari exploration (if/when BiDi support lands)

---

## Differentiators vs Existing Go Libraries

| Feature | [chromedp][chromedp] | [rod][rod] | **navigo** |
|---------|----------|-----|-----------|
| Architecture | DOM node ID (broken for iframes) | Remote object ID | Remote object ID |
| Auto-wait | No | Yes | Yes |
| Accessibility tree | Raw CDP only | No | First-class, AI-optimized, with stable refs |
| Stable element refs | No | No | Yes (`ref=e1`, `ref=e2`) |
| Structured extraction | No | No | Yes (Go generics + struct tags) |
| MCP server | No | [Separate pkg][rod-mcp] | Built-in (37 tools) |
| Stealth mode | No | Separate pkg | Built-in (13 evasions) |
| Cross-browser | No | No | CDP + BiDi (Phase 7) |
| Connection pool | No | No | Built-in with rate limiting |
| Event handling | Fixed buffer (deadlocks) | goob (safe) | Channel-based per-session (safe) |
| Browser management | System browser (version mismatch risk) | Ships specific Chromium | Both modes (system + download) |
| WebSocket | [gobwas/ws][gobwas-ws] (external dep) | Custom ~180 LOC (pluggable) | Custom minimal (pluggable) |
| External deps | 2 (gobwas/ws, gobwas/httphead) | 0 | 0 (or 1 for MCP server framework) |
| Network interception | Yes (Network domain only) | Yes (hijack) | Yes (Network + Fetch domains) |
| iframe support | Broken ([open issue 3+ years][chromedp-iframe]) | Works (remote object ID) | Works (remote object ID + cross-frame AX tree) |

---

## Prior Art & Lessons

| Project | Lesson Learned |
|---------|---------------|
| [chromedp][chromedp] | DOM node ID architecture is a dead end for iframes and complex pages. Don't repeat. |
| [rod][rod] | Remote object ID + auto-wait + shipped browser + pluggable WebSocket = right foundation. |
| [Playwright MCP][playwright-mcp] | Accessibility snapshots with stable refs = the proven agent interaction model (50+ tools, vision + snapshot modes). |
| [browser-use][browser-use] | Raw CDP > framework wrappers for AI agent performance ([blog post][browser-use-cdp-post]). |
| [Stagehand v3][stagehand] | Dropped Playwright for CDP — confirms raw CDP is the right choice for agent workloads. |
| [rod-mcp][rod-mcp] | 12-tool minimal MCP proves Go + CDP + accessibility snapshots works. Our 37-tool design is the full version. |
| [chrome-devtools-mcp][chrome-devtools-mcp] | 34 tools including network inspection, performance tracing, extensions — shows what a complete MCP looks like. |
| [agent-browser][agent-browser] | Rust CLI with accessibility tree and stable refs achieves 93% fewer tokens than DOM approaches. |
| [Lightpanda][lightpanda] | Alternative CDP-compatible browsers exist — don't assume Chrome binary. Design for any CDP endpoint. |
| bayhome | Real-world CDP+MCP usage in our own workspace: anti-bot research (PerimeterX), Chrome extension patterns, session reuse. Validates the connect-to-existing-browser use case. |

---

## References

### Go Libraries
[chromedp]: https://github.com/chromedp/chromedp
[cdproto]: https://github.com/chromedp/cdproto
[cdproto-gen]: https://github.com/chromedp/cdproto-gen
[rod]: https://github.com/go-rod/rod
[rod-mcp]: https://github.com/go-rod/rod-mcp
[playwright-go]: https://github.com/playwright-community/playwright-go
[gowebdriver]: https://github.com/hupe1980/gowebdriver
[gobwas-ws]: https://github.com/gobwas/ws
[gorilla-ws]: https://github.com/gorilla/websocket
[coder-ws]: https://github.com/coder/websocket
[x-net-ws]: https://pkg.go.dev/golang.org/x/net/websocket
[chromedp-iframe]: https://github.com/chromedp/chromedp/issues/72

### Browser Protocols
[cdp-spec]: https://chromedevtools.github.io/devtools-protocol/
[cdp-domains]: https://chromedevtools.github.io/devtools-protocol/tot/
[cdp-target]: https://chromedevtools.github.io/devtools-protocol/tot/Target/
[cdp-page]: https://chromedevtools.github.io/devtools-protocol/tot/Page/
[cdp-runtime]: https://chromedevtools.github.io/devtools-protocol/tot/Runtime/
[cdp-dom]: https://chromedevtools.github.io/devtools-protocol/tot/DOM/
[cdp-input]: https://chromedevtools.github.io/devtools-protocol/tot/Input/
[cdp-network]: https://chromedevtools.github.io/devtools-protocol/tot/Network/
[cdp-fetch]: https://chromedevtools.github.io/devtools-protocol/tot/Fetch/
[cdp-accessibility]: https://chromedevtools.github.io/devtools-protocol/tot/Accessibility/
[cdp-emulation]: https://chromedevtools.github.io/devtools-protocol/tot/Emulation/
[cdp-browser]: https://chromedevtools.github.io/devtools-protocol/tot/Browser/
[cdp-storage]: https://chromedevtools.github.io/devtools-protocol/tot/Storage/
[cdp-css]: https://chromedevtools.github.io/devtools-protocol/tot/CSS/
[cdp-log]: https://chromedevtools.github.io/devtools-protocol/tot/Log/
[chromium-pdl]: https://chromium.googlesource.com/chromium/src/+/main/third_party/blink/public/devtools_protocol/browser_protocol.pdl
[chromium-ws-fragment]: https://issues.chromium.org/issues/40125913
[bidi-spec]: https://w3c.github.io/webdriver-bidi/
[firefox-cdp-deprecated]: https://fxdx.dev/deprecating-cdp-support-in-firefox-embracing-the-future-with-webdriver-bidi/

### AI Agent Projects
[browser-use]: https://github.com/browser-use/browser-use
[browser-use-cdp-post]: https://browser-use.com/posts/playwright-to-cdp
[stagehand]: https://github.com/browserbase/stagehand
[playwright-mcp]: https://github.com/microsoft/playwright-mcp
[chrome-devtools-mcp]: https://www.npmjs.com/package/@anthropic-ai/chrome-devtools-mcp
[agent-browser]: https://github.com/vercel-labs/agent-browser
[lightpanda]: https://lightpanda.io/
[puppeteer-stealth]: https://github.com/nicedoc/nicedoc.io

### MCP Protocol
[mcp-spec]: https://spec.modelcontextprotocol.io/
[jsonrpc]: https://www.jsonrpc.org/specification
[mcp-go]: https://github.com/mark3labs/mcp-go
[mcp-go-sdk]: https://github.com/modelcontextprotocol/go-sdk
