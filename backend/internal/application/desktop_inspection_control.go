package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// RequestInspectionControl uses either the real connected SDK transport or
// this same in-process CLI. Polling does not open another Session/ASK.
func (d *DesktopSession) RequestInspectionControl(ctx context.Context, request protocol.JSONObject, timeout time.Duration) (json.RawMessage, error) {
	d.mu.Lock()
	state, closed, engine := d.sdk, d.closed, d.engine
	d.mu.Unlock()
	if closed || engine == nil {
		return nil, errors.New("CLI session is not running")
	}
	if state != nil {
		state.mu.Lock()
		connected := state.send != nil && !state.closed
		state.mu.Unlock()
		if connected {
			data, err := request.MarshalJSON()
			if err != nil {
				return nil, err
			}
			data, err = d.RequestSDKControl(ctx, data, timeout, nil)
			if err != nil {
				return nil, err
			}
			return data, nil
		}
	}
	d.runtimeMu.Lock()
	defer d.runtimeMu.Unlock()
	d.mu.Lock()
	closed, engine = d.closed, d.engine
	d.mu.Unlock()
	if closed || engine == nil {
		return nil, errors.New("CLI session is not running")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch request.String("subtype") {
	case "get_context_usage":
		value, err := engine.process.ReadContextUsage(ctx, string(request.Get("estimateOnly")) == "true")
		if err != nil {
			return nil, err
		}
		return value.MarshalJSON()
	case "get_session_usage":
		value, err := engine.process.ReadSessionUsage(ctx)
		if err != nil {
			return nil, err
		}
		return value.MarshalJSON()
	case "mcp_status":
		value, err := engine.process.MCPControl(ctx, "mcp_status", "")
		if err != nil {
			return nil, err
		}
		return value.MarshalJSON()
	default:
		// The context analyser is bound separately to the original producer;
		// never substitute token totals for its category/detail response.
		return nil, errors.New("context analyser is not available")
	}
}

var _ interface {
	RequestInspectionControl(context.Context, protocol.JSONObject, time.Duration) (json.RawMessage, error)
} = (*DesktopSession)(nil)
