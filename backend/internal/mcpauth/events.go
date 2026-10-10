package mcpauth

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) flowUUID() (string, error) {
	data := make([]byte, 16)
	if _, err := io.ReadFull(s.options.Random, data); err != nil {
		return "", err
	}
	data[6] = (data[6] & 15) | 64
	data[8] = (data[8] & 63) | 128
	text := hex.EncodeToString(data)
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}
func flowFields(config protocol.JSONObject) protocol.JSONObject {
	result := record(field("transportType", config.String("type")))
	if u, err := parseURL(config.String("url")); err == nil {
		u.SetSearch("")
		result.Set("mcpServerBaseUrl", quote(strings.TrimSuffix(u.Href(false), "/")))
	}
	return result
}
func (s *Service) flowEnd(config protocol.JSONObject, id string, codeObtained bool, err error) {
	fields := flowFields(config)
	fields.Set("flowAttemptId", quote(id))
	if err == nil {
		s.event("tengu_mcp_oauth_flow_success", fields)
		return
	}
	reason := "unknown"
	var cancelled AuthenticationCancelledError
	if errors.As(err, &cancelled) || errors.Is(err, context.Canceled) {
		reason = "cancelled"
	} else if codeObtained {
		reason = "token_exchange_failed"
	} else {
		message := err.Error()
		switch {
		case strings.Contains(message, "Authentication timeout"):
			reason = "timeout"
		case strings.Contains(message, "OAuth state mismatch"):
			reason = "state_mismatch"
		case strings.Contains(message, "OAuth error:"):
			reason = "provider_denied"
		case strings.Contains(message, "already in use") || strings.Contains(message, "EADDRINUSE") || strings.Contains(message, "callback server failed") || strings.Contains(message, "No available port"):
			reason = "port_unavailable"
		case strings.Contains(message, "SDK auth failed"):
			reason = "sdk_auth_failed"
		}
	}
	fields.Set("reason", quote(reason))
	var oauth *OAuthError
	if errors.As(err, &oauth) {
		fields.Set("error_code", quote(oauth.Code))
		if match := regexp.MustCompile(`^HTTP ([0-9]{3}):`).FindStringSubmatch(oauth.Message); match != nil {
			fields.Set("http_status", quote(match[1]))
		}
	}
	s.event("tengu_mcp_oauth_flow_error", fields)
}
