package cdp

import "context"

type Transport interface {
	Send(ctx context.Context, data []byte) error
	Read(ctx context.Context) ([]byte, error)
	Close() error
}
