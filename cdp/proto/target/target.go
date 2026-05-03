// Package target provides Chrome DevTools Protocol commands and types
// for the Target domain.
package target

import (
	"context"
	"fmt"
)

const (
	CommandCreateTarget   = "Target.createTarget"
	CommandAttachToTarget = "Target.attachToTarget"
)

type Caller interface {
	Call(ctx context.Context, method string, params any, result any) error
}

type TargetID string
type SessionID string

type CreateTargetParams struct {
	URL string `json:"url"`
}

type CreateTargetResult struct {
	TargetID TargetID `json:"targetId"`
}

func CreateTarget(url string) *CreateTargetParams {
	return &CreateTargetParams{URL: url}
}

func (p *CreateTargetParams) Do(ctx context.Context, caller Caller) (*CreateTargetResult, error) {
	if caller == nil {
		return nil, fmt.Errorf("execute %s: nil caller", CommandCreateTarget)
	}

	var result CreateTargetResult
	if err := caller.Call(ctx, CommandCreateTarget, p, &result); err != nil {
		return nil, fmt.Errorf("execute %s: %w", CommandCreateTarget, err)
	}

	return &result, nil
}

type AttachToTargetParams struct {
	TargetID TargetID `json:"targetId"`
	Flatten  bool     `json:"flatten,omitempty"`
}

type AttachToTargetResult struct {
	SessionID SessionID `json:"sessionId"`
}

func AttachToTarget(targetID TargetID) *AttachToTargetParams {
	return &AttachToTargetParams{TargetID: targetID}
}

func (p AttachToTargetParams) WithFlatten(flatten bool) *AttachToTargetParams {
	p.Flatten = flatten
	return &p
}

func (p *AttachToTargetParams) Do(ctx context.Context, caller Caller) (*AttachToTargetResult, error) {
	if caller == nil {
		return nil, fmt.Errorf("execute %s: nil caller", CommandAttachToTarget)
	}

	var result AttachToTargetResult
	if err := caller.Call(ctx, CommandAttachToTarget, p, &result); err != nil {
		return nil, fmt.Errorf("execute %s: %w", CommandAttachToTarget, err)
	}

	return &result, nil
}
