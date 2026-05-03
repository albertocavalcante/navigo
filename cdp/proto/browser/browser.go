// Package browser provides Chrome DevTools Protocol commands and types
// for the Browser domain.
package browser

import (
	"context"
	"fmt"

	"github.com/albertocavalcante/navigo/cdp"
)

const (
	// CommandGetVersion is the Browser.getVersion CDP method name.
	CommandGetVersion = "Browser.getVersion"
)

type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

// GetVersionParams contains the parameters for Browser.getVersion.
// The method currently accepts no parameters, but the type keeps the
// command surface generator-friendly.
type GetVersionParams struct{}

// GetVersionResult is the response payload for Browser.getVersion.
type GetVersionResult struct {
	ProtocolVersion   string `json:"protocolVersion"`
	Product           string `json:"product"`
	Revision          string `json:"revision"`
	UserAgent         string `json:"userAgent"`
	JavaScriptVersion string `json:"jsVersion"`
}

// GetVersion creates a Browser.getVersion command.
func GetVersion() *GetVersionParams {
	return &GetVersionParams{}
}

// Do executes Browser.getVersion against the provided caller.
func (p *GetVersionParams) Do(ctx context.Context, caller Caller) (*GetVersionResult, error) {
	if caller == nil {
		return nil, fmt.Errorf("execute %s: nil caller", CommandGetVersion)
	}

	var result GetVersionResult
	if err := caller.Call(ctx, CommandGetVersion, p, &result); err != nil {
		return nil, fmt.Errorf("execute %s: %w", CommandGetVersion, err)
	}

	return &result, nil
}

var _ Caller = (*cdp.Session)(nil)
