package modelyield

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/apipresentation"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestretry"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func mediaPresentationOptions(input requestretry.Input, history []protocol.JSONObject) apipresentation.MediaOptions {
	return apipresentation.MediaOptions{Model: input.Context.Options.Model, NonInteractive: input.Context.Options.IsNonInteractiveSession, MessagesForAPI: history}
}
func captureMediaHistory(history []protocol.JSONObject) ([]protocol.JSONObject, error) {
	values := make([]protocol.JSONObject, len(history))
	for i, record := range history {
		raw, err := record.MarshalJSON()
		if err != nil {
			return nil, err
		}
		if err := values[i].UnmarshalJSON(raw); err != nil {
			return nil, err
		}
	}
	return values, nil
}
func nativeMediaStreamTerminal(failure error, domain apipresentation.Domain, factory *messages.Factory, input requestretry.Input, history []protocol.JSONObject) (*streamhistory.Envelope, error) {
	value, matched, err := apipresentation.NativeMediaFailure(failure, domain, factory, mediaPresentationOptions(input, history))
	if err != nil || matched {
		return value, err
	}
	return apipresentation.NativeStreamFailure(failure, domain, factory)
}
