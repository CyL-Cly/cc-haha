package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// CreateModelValidationMessage is the SDK JSON consumer with original error
// identities. The sealed caller owns auth, purpose, native scope and retries.
func (client *Anthropic) CreateModelValidationMessage(ctx context.Context, params protocol.JSONObject) (MessageResponse, error) {
	return client.CreateModelValidationMessageObserved(ctx, params, nil)
}

// The observation is the actual beta JSON encoder output before physical Send.
// It cannot change the body, auth, headers, retry policy or model conclusion.
func (client *Anthropic) CreateModelValidationMessageObserved(ctx context.Context, params protocol.JSONObject, observe func(*MessagesRequest)) (MessageResponse, error) {
	copy := *client
	copy.config.modelValidationPrepared = observe
	copy.config.modelValidationJSON = true
	response, err := copy.CreateBetaMessage(ctx, params)
	if err != nil {
		var sdk *SDKStreamError
		if errors.As(err, &sdk) || errors.Is(err, ErrHTTPExchangeProfile) || IsOwnerWaitFailure(err) {
			return response, err
		}
		switch err.Error() {
		case "Connection error.":
			return response, &SDKStreamError{Kind: "request_connection", Class: "APIConnectionError", Name: "Error", Message: err.Error(), MessageJSON: []byte(`"Connection error."`), Type: json.RawMessage(`null`), Cause: err}
		case "Request timed out.":
			return response, &SDKStreamError{Kind: "request_connection", Class: "APIConnectionTimeoutError", Name: "Error", Message: err.Error(), MessageJSON: []byte(`"Request timed out."`), Type: json.RawMessage(`null`), Cause: err}
		case "Request was aborted.":
			return response, &SDKStreamError{Kind: "caller_abort", Class: "APIUserAbortError", Name: "Error", Message: err.Error(), MessageJSON: []byte(`"Request was aborted."`), Type: json.RawMessage(`null`), Cause: err}
		}
	}
	return response, err
}

func finishModelValidationJSON(raw []byte, status int) (MessageResponse, error) {
	value := bytes.TrimSpace(raw)
	if status == 204 || bytes.Equal(value, []byte("null")) {
		return MessageResponse{}, errors.New("null is not an object (evaluating 'response._request_id')")
	}
	if len(value) == 0 {
		return MessageResponse{}, errors.New("undefined is not an object (evaluating 'response._request_id')")
	}
	if !json.Valid(value) {
		return MessageResponse{}, errors.New("Failed to parse JSON")
	}
	if value[0] != '{' && value[0] != '[' {
		return MessageResponse{}, errors.New("Attempted to assign to readonly property.")
	}
	return MessageResponse{Raw: raw}, nil
}
