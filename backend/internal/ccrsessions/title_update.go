package ccrsessions

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessiontitle"
)

// QueueTitleUpdate preserves useRemoteSession's fire-and-forget first-input
// producer. Both the canonical query and PATCH remain owned and joined at close.
func (m *Manager) QueueTitleUpdate(description json.RawMessage) error {
	if m.service.options.GenerateSessionTitle == nil {
		return errors.New("CCR first-input title producer unavailable")
	}
	if string(description) == `""` {
		return nil
	}
	ctx, done, err := m.operation(context.Background())
	if err != nil {
		return err
	}
	description = append(json.RawMessage(nil), description...)
	go func() {
		defer done()
		title, failure := m.service.options.GenerateSessionTitle(ctx, description)
		if ctx.Err() != nil {
			return
		}
		if failure != nil {
			m.failure(failure)
		}
		if title == nil {
			fallback, failure := sessiontitle.TruncateRaw(description, 75)
			if failure != nil {
				m.failure(failure)
				return
			}
			title = fallback
		}
		credentials, err := m.service.credentials(ctx, true)
		if err != nil {
			m.failure(err)
			return
		}
		body, _ := protocol.NewJSONObject(protocol.JSONField{Name: "title", Value: title}).MarshalJSON()
		_, _, err = m.service.request(ctx, "PATCH", "/v1/sessions/"+m.sid, body, credentials, true, 0, nil)
		m.failure(err)
	}()
	return nil
}
