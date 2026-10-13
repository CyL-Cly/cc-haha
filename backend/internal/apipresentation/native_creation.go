package apipresentation

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

// Native creation failures keep upstream HTTP identities for the real query
// loop and team continuation. A 503 must not become a missing-capability fault.
func NativeCreationFailure(failure error, domain Domain, factory *messages.Factory, input MediaOptions) (*streamhistory.Envelope, error) {
	if !domain.direct || factory == nil || absent(failure) {
		return nil, unsupported("native creation error domain/factory")
	}
	if retry, ok := failure.(*retrypolicy.CannotRetryError); ok {
		failure = retry.Original
	}
	if original, ok := failure.(interface{ APIMessage() string }); ok {
		record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": quote("API Error: " + original.APIMessage()), "error": quote("unknown")})
		if err != nil {
			return nil, err
		}
		return streamFailureEnvelope(record)
	}
	var sdk *provider.SDKStreamError
	if !errors.As(failure, &sdk) || sdk == nil {
		return CreationFailure(failure, domain, factory)
	}
	if sdk.Status == nil {
		return nativeNoStatusFailure(sdk, factory)
	}
	detail, err := apiDetail(sdk)
	if err != nil {
		return nil, err
	}
	text := interpolate("API Error: "+strconv.Itoa(*sdk.Status)+" ", detail, "")
	typeName := "unknown"
	if *sdk.Status == 401 || *sdk.Status == 403 {
		typeName = "authentication_failed"
		prefix := "Please run /login · API Error: "
		if input.NonInteractive {
			prefix = "Failed to authenticate. API Error: "
		}
		text = interpolate(prefix+strconv.Itoa(*sdk.Status)+" ", detail, "")
	}
	if *sdk.Status == 404 {
		typeName = "invalid_request"
		command := "/model"
		if input.NonInteractive {
			command = "--model"
		}
		text = quote("There's an issue with the selected model (" + input.Model + "). It may not exist or you may not have access to it. Run " + command + " to pick a different model.")
	}
	record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": text, "error": quote(typeName)})
	if err != nil {
		return nil, err
	}
	return streamFailureEnvelope(record)
}

func nativeNoStatusFailure(sdk *provider.SDKStreamError, factory *messages.Factory) (*streamhistory.Envelope, error) {
	message := sdk.MessageJSON
	if message == nil {
		message = quote(sdk.Message)
	}
	if _, err := protocol.DecodeJavaScriptJSONString(message); err != nil {
		return nil, err
	}
	var text json.RawMessage
	switch {
	case sdk.Class == "APIConnectionTimeoutError" || sdk.Class == "APIConnectionError" && strings.Contains(strings.ToLower(sdk.Message), "timeout"):
		text = quote("Request timed out")
	case sdk.Class == "APIError":
		detail, err := apiDetail(sdk)
		if err != nil {
			return nil, err
		}
		text = interpolate("API Error: ", detail, "")
	case sdk.Class == "APIConnectionError":
		code, err := connectionCode(sdk)
		if err != nil {
			return nil, err
		}
		var value string
		_ = json.Unmarshal(code, &value)
		if value == "ETIMEDOUT" || nativeSSLCode(value) || sdk.Message == "Connection error." {
			text = connectionText(code)
		} else {
			// Source sanitizes HTML transport messages while retaining ordinary
			// Unicode text. A real transport failure is never a capability error.
			units, _ := protocol.DecodeJavaScriptJSONString(message)
			if sanitized := sanitizeErrorHTML(units); len(sanitized) > 0 {
				message = protocol.QuoteJavaScriptUTF16(sanitized)
			}
			text = interpolate("API Error: ", message, "")
		}
	default:
		text = interpolate("API Error: ", message, "")
	}
	record, err := factory.CreateOrderedAssistantAPIError(storage.Object{"content": text, "error": quote("unknown")})
	if err != nil {
		return nil, err
	}
	return streamFailureEnvelope(record)
}

func nativeSSLCode(value string) bool {
	// This is Source's SSL_ERROR_CODES, not a substring classification.
	switch value {
	case "UNABLE_TO_VERIFY_LEAF_SIGNATURE", "UNABLE_TO_GET_ISSUER_CERT", "UNABLE_TO_GET_ISSUER_CERT_LOCALLY", "CERT_HAS_EXPIRED", "CERT_REVOKED", "DEPTH_ZERO_SELF_SIGNED_CERT", "SELF_SIGNED_CERT_IN_CHAIN", "ERR_TLS_CERT_ALTNAME_INVALID", "HOSTNAME_MISMATCH", "CERT_NOT_YET_VALID", "CERT_SIGNATURE_FAILURE", "CERT_REJECTED", "CERT_UNTRUSTED", "CERT_CHAIN_TOO_LONG", "PATH_LENGTH_EXCEEDED", "ERR_TLS_HANDSHAKE_TIMEOUT", "ERR_SSL_WRONG_VERSION_NUMBER", "ERR_SSL_DECRYPTION_FAILED_OR_BAD_RECORD_MAC":
		return true
	}
	return false
}
