package resumestate

import (
	"encoding/json"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

// Prepare consumes the independently loaded/restored history. Host options,
// system/tools and runtime policy remain original caller inputs. The prepared
// history is retained once across actual retry attempts by requestcontext.
func Prepare(result *Result, input requestcontext.Context, dependencies requestcontext.Dependencies) (*requestcontext.PreparedContext, error) {
	if result == nil {
		return nil, errors.New("resume request requires a successfully restored conversation")
	}
	if input.History != nil {
		return nil, errors.New("resume request forbids alternate prepared history")
	}
	input.History = make([]protocol.JSONObject, 0, len(result.Messages))
	for _, message := range result.Messages {
		raw, err := message.MarshalJSON()
		if err != nil {
			return nil, err
		}
		var record protocol.JSONObject
		if err := json.Unmarshal(raw, &record); err != nil || !record.IsObject() {
			return nil, &requestcontext.CapabilityError{Boundary: "non-object resumed envelope"}
		}
		input.History = append(input.History, record)
	}
	return requestcontext.Prepare(input, dependencies)
}
