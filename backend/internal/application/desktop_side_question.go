package application

import (
	"context"
	"errors"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
)

func (d *DesktopSession) RequestSideQuestionControl(ctx context.Context, request protocol.JSONObject, timeout time.Duration) (protocol.JSONObject, error) {
	d.mu.Lock()
	state, closed, engine := d.sdk, d.closed, d.engine
	d.mu.Unlock()
	if closed || engine == nil {
		return protocol.JSONObject{}, errors.New("CLI session is not running")
	}
	if state != nil {
		state.mu.Lock()
		connected := state.send != nil && !state.closed
		state.mu.Unlock()
		if connected {
			data, e := request.MarshalJSON()
			if e != nil {
				return protocol.JSONObject{}, e
			}
			result, e := d.RequestSDKControl(ctx, data, timeout, nil)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			var value protocol.JSONObject
			e = value.UnmarshalJSON(result)
			return value, e
		}
	}
	active, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return engine.process.SideQuestionControl(active, request)
}

var _ server.SideQuestionControlSession = (*DesktopSession)(nil)
