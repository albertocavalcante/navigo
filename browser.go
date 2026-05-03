package navigo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/albertocavalcante/navigo/cdp"
	cdpbrowser "github.com/albertocavalcante/navigo/cdp/proto/browser"
	"github.com/albertocavalcante/navigo/launcher"
)

type Browser struct {
	conn    *cdp.Conn
	version launcher.Version
	cancel  context.CancelFunc
	done    chan error
	waitMu  sync.Mutex
	waited  bool
}

func Connect(ctx context.Context, rawURL string) (*Browser, error) {
	conn, version, err := (launcher.Remote{}).Connect(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("connect browser: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	browser := &Browser{
		conn:    conn,
		version: version,
		cancel:  cancel,
		done:    make(chan error, 1),
	}

	go func() {
		browser.done <- conn.Run(runCtx)
	}()

	return browser, nil
}

func (b *Browser) CDP() *cdp.Conn {
	return b.conn
}

func (b *Browser) Version() launcher.Version {
	return b.version
}

func (b *Browser) GetVersion(ctx context.Context) (*cdpbrowser.GetVersionResult, error) {
	result, err := cdpbrowser.GetVersion().Do(ctx, b.conn.RootSession())
	if err != nil {
		return nil, fmt.Errorf("get browser version: %w", err)
	}

	return result, nil
}

func (b *Browser) Close() error {
	b.cancel()
	if err := b.conn.Close(); err != nil {
		return fmt.Errorf("close browser connection: %w", err)
	}

	return b.wait()
}

func (b *Browser) Wait() error {
	return b.wait()
}

func (b *Browser) wait() error {
	b.waitMu.Lock()
	defer b.waitMu.Unlock()

	if b.waited {
		return nil
	}

	err := <-b.done
	b.waited = true
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}

	return err
}
