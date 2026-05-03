package cdp

import (
	"encoding/json"
	"fmt"
)

type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	return fmt.Sprintf("cdp error %d: %s", e.Code, e.Message)
}
