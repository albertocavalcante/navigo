# Navigo - Browser Automation SDK for Go

> Pure Go browser automation library built for agentic AI and scraping workloads.
> Equal or better than Puppeteer/Playwright for Go developers.

## Research Summary

### Existing Go Landscape

| Library | Stars | Architecture | Cross-browser | AI-ready | Verdict |
|---------|-------|-------------|---------------|----------|---------|
| **chromedp** | 13k | DOM node ID based | Chrome only | No | Production-proven but architecturally limited (no iframe support, fixed event buffer deadlocks, no auto-wait) |
| **rod** | 6.8k | Remote object ID based | Chrome only | No (rod-mcp exists separately) | Better architecture than chromedp, auto-wait, thread-safe, but still Chrome-only |
| **playwright-go** | ~2k | Go wrapper over Node.js process | Chromium+Firefox+WebKit | No | Not pure Go - requires Node.js runtime (~50MB overhead), community-maintained |
| **gowebdriver** | <1k | WebDriver BiDi | Chrome+Firefox | No | Only Go BiDi impl, dormant since mid-2024 |

### Browser Protocols

| Protocol | Status | Browsers | Future |
|----------|--------|----------|--------|
| **CDP** (Chrome DevTools Protocol) | Mature, dominant | Chrome/Chromium/Edge | Will continue in Chromium but not standardized |
| **WebDriver BiDi** | W3C standard in progress | Chrome, Firefox, Edge (Safari planned) | The future - cross-browser, bidirectional, standardized |
| **Firefox CDP** | Deprecated | Firefox (disabled by default since v129) | Dead - Firefox is BiDi-only going forward |
| **Safari/WebKit** | WebKit Debug Protocol | Safari | Apple participates in BiDi working group; no CDP |

### Industry Trends (2025-2026)

1. **Raw CDP over frameworks**: browser-use (91k stars) and Stagehand v3 both dropped Playwright for raw CDP - 5x and 44% perf improvements respectively
2. **Accessibility tree as primary representation**: ~200-400 tokens vs 3,000-5,000 for DOM. All major agent frameworks use it
3. **Stable element refs**: `@e1`, `@e2` pattern from Playwright MCP - deterministic, LLM-friendly
4. **Hybrid vision + accessibility**: Screenshots supplement accessibility tree for canvas/charts
5. **Lightweight headless browsers**: Lightpanda (Zig-based, 11x faster, CDP-compatible) emerging for agent workloads
6. **MCP as the integration layer**: Playwright MCP, rod-mcp, chrome-devtools-mcp all exist

---

## Design

### Name: `navigo`

*Navigate + Go. Direct, memorable, available.*

### Goals

1. **Pure Go, zero external deps** - no Node.js, no CGo, stdlib + own packages only
2. **CDP-first, BiDi-ready** - raw CDP for Chrome (the 95% case), BiDi adapter for Firefox cross-browser
3. **Agent-native** - accessibility tree, stable refs, token-optimized snapshots as first-class citizens
4. **Scraping-grade** - stealth mode, anti-detection, connection to existing browser sessions, cookie management
5. **Production-scale** - concurrent sessions, connection pooling, graceful lifecycle management
6. **MCP server included** - ship a ready-to-use MCP server binary alongside the library

### Architecture

```
navigo/
├── cdp/                    # Low-level CDP client (WebSocket, JSON-RPC)
│   ├── conn.go             # WebSocket connection with reconnect
│   ├── session.go          # CDP session multiplexing
│   ├── transport.go        # Transport interface (WebSocket, pipe)
│   └── proto/              # Auto-generated CDP domain types + commands
│       ├── gen.go           # Code generator from CDP protocol spec
│       ├── page/
│       ├── dom/
│       ├── network/
│       ├── runtime/
│       ├── accessibility/
│       ├── input/
│       ├── emulation/
│       ├── target/
│       ├── fetch/           # Request interception
│       ├── storage/
│       └── ...
│
├── bidi/                   # WebDriver BiDi client (future, cross-browser)
│   ├── conn.go
│   ├── session.go
│   └── proto/              # BiDi protocol types
│
├── browser.go              # Browser lifecycle (launch, connect, close)
├── context.go              # Browser context (isolation, cookies, storage)
├── page.go                 # Page operations (navigate, screenshot, PDF, eval)
├── element.go              # Element handle (click, type, select, upload)
├── frame.go                # Frame/iframe handling
├── input.go                # Keyboard + mouse simulation
├── network.go              # Network interception, request/response capture
├── wait.go                 # Auto-wait strategies (visible, attached, stable)
├── selector.go             # CSS + XPath + text + accessibility role selectors
│
├── ax/                     # Accessibility tree (the AI layer)
│   ├── tree.go             # Full AX tree with pruning/filtering
│   ├── snapshot.go         # Token-optimized page snapshot with stable refs
│   ├── ref.go              # Stable element references (@e1, @e2, ...)
│   ├── serialize.go        # Markdown/structured serialization for LLMs
│   └── extract.go          # Structured data extraction (Go generics)
│
├── stealth/                # Anti-detection
│   ├── stealth.go          # Applies all evasions
│   ├── webdriver.go        # navigator.webdriver = false
│   ├── plugins.go          # Fake plugin array
│   ├── webgl.go            # WebGL vendor/renderer spoofing
│   ├── chrome.go           # window.chrome object
│   ├── permissions.go      # Permissions API override
│   ├── headless.go         # Headless detection evasion
│   └── fingerprint.go      # Canvas/AudioContext fingerprint noise
│
├── launcher/               # Browser binary management
│   ├── launcher.go         # Find/download/launch Chrome
│   ├── download.go         # Download specific Chrome version
│   ├── flags.go            # Chrome CLI flags builder
│   └── remote.go           # Connect to existing browser (debug port)
│
├── pool/                   # Connection pooling for concurrent scraping
│   ├── pool.go             # Browser instance pool
│   └── limiter.go          # Rate limiting, concurrency control
│
├── mcp/                    # MCP server implementation
│   ├── server.go           # MCP stdio server
│   ├── tools.go            # Tool definitions (40+ tools)
│   └── snapshot.go         # Snapshot-based interaction for agents
│
├── cmd/
│   └── navigo-mcp/         # MCP server binary
│       └── main.go
│
└── examples/
    ├── screenshot/
    ├── scrape/
    ├── agent/
    └── stealth/
```

### Core Design Decisions

#### 1. Remote Object ID architecture (like Rod/Puppeteer, not chromedp)

chromedp uses DOM node IDs which break across frames and limit abstractions.
We use remote object IDs (Runtime.RemoteObjectId) which are stable across navigations within a context.

```go
// Element backed by remote object, not DOM node ID
type Element struct {
    objectID runtime.RemoteObjectID
    frame    *Frame
    page     *Page
}
```

#### 2. Auto-waiting built in

Every element interaction auto-waits for the element to be:
- Attached to DOM
- Visible (not `display:none` or zero-size)
- Stable (no animations/transitions in progress)
- Enabled (not disabled)
- Not obscured by other elements

```go
// No manual waits needed - click auto-waits
err := page.Click("#submit")

// Explicit wait when needed
el, err := page.WaitFor("#result", navigo.Visible, navigo.Timeout(5*time.Second))
```

#### 3. Accessibility tree as first-class citizen

The `ax` package provides AI-optimized page representation:

```go
// Get token-optimized page snapshot
snap, err := page.Snapshot()
// snap.Markdown() returns ~200-400 tokens, e.g.:
// - navigation "Main Menu"
//   - link "Home" [@e1]
//   - link "Products" [@e2]
// - main
//   - heading "Welcome" (level=1)
//   - textbox "Search..." [@e3]
//   - button "Search" [@e4]

// Interact via stable refs
err = snap.Click("@e4")
err = snap.Type("@e3", "golang browser")

// Structured extraction with generics
type Product struct {
    Name  string  `ax:"heading"`
    Price float64 `ax:"text,pattern=\\$([\\d.]+)"`
    Link  string  `ax:"link,attr=href"`
}
products, err := ax.Extract[Product](page, "article.product")
```

#### 4. Network layer with full interception

```go
// Intercept and modify requests
page.OnRequest(func(req *navigo.Request) {
    if req.ResourceType == "image" {
        req.Abort() // block images for speed
        return
    }
    req.Continue()
})

// Capture responses
page.OnResponse(func(resp *navigo.Response) {
    if resp.URL.Path == "/api/data" {
        body, _ := resp.Body()
        // process API response
    }
})
```

#### 5. Stealth by default for scraping

```go
// Stealth mode applies all anti-detection measures
browser, err := navigo.Launch(
    navigo.Stealth(),        // all evasions
    navigo.Headless(),       // headful-like headless
    navigo.ProxyServer(url), // proxy support
)

// Or connect to existing browser (preserves cookies/sessions)
browser, err := navigo.Connect("http://localhost:9222")
```

#### 6. Concurrent session pool

```go
// Pool of browser contexts for high-throughput scraping
pool, err := navigo.NewPool(navigo.PoolSize(10))
defer pool.Close()

// Acquire a context from the pool
err = pool.Do(ctx, func(page *navigo.Page) error {
    return page.Navigate("https://example.com")
    // page returned to pool after function returns
})
```

#### 7. MCP server with 40+ tools

The `navigo-mcp` binary provides a complete MCP server:

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

**Tool categories:**
- **Navigation**: `navigate`, `back`, `forward`, `reload`, `new_tab`, `close_tab`, `list_tabs`
- **Snapshot**: `snapshot` (accessibility tree), `screenshot`, `pdf`
- **Interaction**: `click`, `type`, `select`, `hover`, `drag`, `scroll`, `press_key`
- **Extraction**: `extract` (structured), `evaluate` (JS), `query` (CSS/XPath)
- **Network**: `intercept`, `list_requests`, `get_response`, `set_cookies`, `get_cookies`
- **Wait**: `wait_for`, `wait_for_navigation`, `wait_for_network_idle`
- **Emulation**: `set_viewport`, `set_user_agent`, `set_geolocation`, `throttle_network`

### API Design

#### Core API

```go
package navigo

// Launch a new browser
func Launch(opts ...LaunchOption) (*Browser, error)

// Connect to existing browser
func Connect(url string, opts ...ConnectOption) (*Browser, error)

// Browser
type Browser struct { ... }
func (b *Browser) NewContext(opts ...ContextOption) (*Context, error)
func (b *Browser) Close() error
func (b *Browser) Version() string

// Context (isolated session - cookies, storage, etc.)
type Context struct { ... }
func (c *Context) NewPage() (*Page, error)
func (c *Context) Pages() []*Page
func (c *Context) Cookies(urls ...string) ([]*Cookie, error)
func (c *Context) SetCookies(cookies ...*Cookie) error
func (c *Context) Close() error

// Page
type Page struct { ... }
func (p *Page) Navigate(url string) error
func (p *Page) URL() string
func (p *Page) Title() (string, error)
func (p *Page) Content() (string, error)
func (p *Page) Screenshot(opts ...ScreenshotOption) ([]byte, error)
func (p *Page) PDF(opts ...PDFOption) ([]byte, error)
func (p *Page) Evaluate(js string, args ...any) (any, error)
func (p *Page) Close() error

// Selectors
func (p *Page) Query(selector string) (*Element, error)
func (p *Page) QueryAll(selector string) ([]*Element, error)
func (p *Page) Click(selector string) error
func (p *Page) Type(selector string, text string) error
func (p *Page) Select(selector string, values ...string) error

// Accessibility
func (p *Page) Snapshot() (*ax.Snapshot, error)

// Waiting
func (p *Page) WaitFor(selector string, opts ...WaitOption) (*Element, error)
func (p *Page) WaitForNavigation() error
func (p *Page) WaitForNetworkIdle(opts ...WaitOption) error

// Network
func (p *Page) OnRequest(fn func(*Request)) error
func (p *Page) OnResponse(fn func(*Response)) error

// Element
type Element struct { ... }
func (e *Element) Click() error
func (e *Element) Type(text string) error
func (e *Element) Text() (string, error)
func (e *Element) Attribute(name string) (string, error)
func (e *Element) BoundingBox() (*Rect, error)
func (e *Element) Screenshot() ([]byte, error)
func (e *Element) ScrollIntoView() error
```

### Implementation Plan

#### Phase 1: CDP Foundation (weeks 1-3)
- [ ] WebSocket transport with auto-reconnect
- [ ] CDP session multiplexing (one WS conn, many sessions)
- [ ] Code generator for CDP protocol types from `browser_protocol.pdl`
- [ ] Generate all CDP domain types (Page, DOM, Network, Runtime, etc.)
- [ ] Browser launcher (find Chrome, launch with flags, connect)
- [ ] Connect to existing browser via debug port

#### Phase 2: Core Page API (weeks 3-5)
- [ ] Page lifecycle (navigate, reload, back, forward)
- [ ] Element handle with remote object ID
- [ ] CSS and XPath selector engine
- [ ] Auto-wait for element states
- [ ] Click, type, select interactions
- [ ] Keyboard and mouse input simulation
- [ ] Frame/iframe handling
- [ ] JavaScript evaluation
- [ ] Screenshots (full page, element, viewport)
- [ ] PDF generation

#### Phase 3: Network & Context (weeks 5-6)
- [ ] Browser context (cookie isolation, storage)
- [ ] Cookie management (get, set, delete)
- [ ] Network request interception (Fetch domain)
- [ ] Request/response capture and inspection
- [ ] File upload/download handling

#### Phase 4: Accessibility & Agent Layer (weeks 6-8)
- [ ] Full accessibility tree extraction via CDP
- [ ] Tree pruning and filtering for LLM consumption
- [ ] Stable element ref system (@e1, @e2)
- [ ] Markdown serialization of page state
- [ ] Structured data extraction with Go generics
- [ ] Snapshot-based interaction (click/type via refs)

#### Phase 5: Stealth & Scraping (weeks 8-9)
- [ ] navigator.webdriver evasion
- [ ] Headless detection evasion
- [ ] WebGL/Canvas fingerprint noise
- [ ] Plugin/permissions spoofing
- [ ] User-agent rotation
- [ ] Proxy support
- [ ] Connection pool with rate limiting

#### Phase 6: MCP Server (weeks 9-10)
- [ ] MCP stdio server using navigo
- [ ] 40+ tool implementations
- [ ] Snapshot-based agent interaction
- [ ] navigo-mcp binary

#### Phase 7: BiDi & Cross-browser (future)
- [ ] WebDriver BiDi protocol client
- [ ] Firefox support via BiDi
- [ ] Unified API across CDP and BiDi backends
- [ ] Safari exploration (if/when BiDi lands)

### Differentiators vs Existing Go Libraries

| Feature | chromedp | rod | **navigo** |
|---------|----------|-----|-----------|
| Architecture | DOM node ID | Remote object ID | Remote object ID |
| Auto-wait | No | Yes | Yes |
| Accessibility tree | Raw CDP only | No | First-class, AI-optimized |
| Stable element refs | No | No | Yes (@e1, @e2) |
| Structured extraction | No | No | Yes (generics) |
| MCP server | No | Separate pkg | Built-in |
| Stealth mode | No | Separate pkg | Built-in |
| Cross-browser | No | No | CDP + BiDi |
| Connection pool | No | No | Built-in |
| Event handling | Fixed buffer (deadlocks) | goob (safe) | Channel-based (safe) |
| Browser management | System browser | Ships Chromium | Both modes |
| Zero deps | Yes | Yes | Yes |

### Naming Alternatives Considered

- `navigo` - Navigate + Go. Clean, memorable, `.go` is in the name. **Winner.**
- `webctl` - Web control. Descriptive but generic.
- `cdpkit` - CDP kit. Too narrow (we want BiDi too).
- `surf` - Short, web-related. Already taken on GitHub.
- `phantom` - Cool but associated with dead PhantomJS.
- `herald` - Doesn't convey browser automation.

### Dependencies

**Zero external dependencies.** Everything in stdlib:
- `net/http` - HTTP client for browser discovery
- `nhooyr.io/websocket` - Actually, we'd vendor or use `golang.org/x/net/websocket`... 
  
Wait - for true zero-deps, we need a WebSocket client. Options:
1. Use `golang.org/x/net/websocket` (x/ packages are quasi-stdlib)
2. Implement minimal WebSocket client (RFC 6455 is not complex for client-side)
3. Accept one dep: `nhooyr.io/websocket` (modern, maintained, 4k stars)

**Decision**: Use `golang.org/x/net/websocket` or implement minimal client. The CDP WebSocket usage is simple (text frames, no compression needed), so a minimal implementation is feasible and keeps us at zero deps.

### Prior Art We Learn From

| Project | Lesson |
|---------|--------|
| **chromedp** | DOM node ID architecture is a dead end for iframes. Don't repeat. |
| **rod** | Remote object ID + auto-wait + shipped browser = right foundation. |
| **Playwright MCP** | Accessibility snapshots with stable refs = the agent interaction model. |
| **browser-use** | Raw CDP > framework wrappers for AI agent performance. |
| **Stagehand v3** | Dropped Playwright for CDP - confirms raw CDP is the right choice. |
| **bayhome** | Real-world CDP+MCP usage, anti-bot research, Chrome extension patterns. |
| **Pinchtab** | ~800 tokens/page proves Go can do efficient accessibility extraction. |
| **Lightpanda** | Alternative CDP-compatible browsers exist - don't assume Chrome binary. |
