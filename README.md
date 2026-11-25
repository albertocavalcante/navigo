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

## Downloads

Enable downloads once, trigger one, then wait. The browser writes GUID-named
files; `WaitForDownload` returns only after the browser reports completion and
the file checks out, then renames it atomically to a sanitised, collision-free
name. Canceled or partial downloads are errors, never successes.

```go
dir := "/path/to/downloads" // must exist
if err := browser.EnableDownloads(ctx, dir); err != nil { /* ... */ }

// ... click a link or navigate to something that downloads ...

ctx, cancel := context.WithTimeout(ctx, time.Minute) // overall limit
defer cancel()
dl, err := browser.WaitForDownload(ctx, navigo.DownloadOptions{
	IdleTimeout:   15 * time.Second, // fail if no progress for this long
	ComputeSHA256: true,
})
switch {
case errors.Is(err, navigo.ErrDownloadCanceled), errors.Is(err, navigo.ErrDownloadIdle):
	// retry or report
case err != nil:
	// timeout, incomplete file, ...
}
fmt.Println(dl.Path, dl.Size, dl.SHA256)
```

## Sign-in detection

`SignInDetector` classifies a URL (or title) as a sign-in page using whole path
segments, so `/login` matches but `/login-history` does not. Defaults are
`login`, `signin`, `sign-in`, `signon`, `auth`, `sso`, `session-timeout`,
`logoff`; override them, add identity-provider hosts or title patterns:

```go
detector := navigo.NewSignInDetector(
	navigo.WithSignInHosts("idp.example.com"),
	navigo.WithSignInTitlePatterns(`(?i)^please sign in`),
)

// Cancel waits as soon as the page lands on a sign-in page.
guarded, release := page.GuardSignIn(ctx, detector)
defer release()
_, err := page.WaitForSelector(guarded, "#report")
if errors.Is(context.Cause(guarded), navigo.ErrSignInRequired) {
	// stop and ask for a fresh session instead of waiting forever
}

// Or check once, or react to every main-frame navigation:
err = page.CheckSignIn(ctx, detector) // errors.Is(err, navigo.ErrSignInRequired)
stop := page.OnNavigation(func(e navigo.NavigationEvent) { log.Println(e.URL) })
defer stop()
```

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
