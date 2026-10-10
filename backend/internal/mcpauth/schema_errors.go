package mcpauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original SDK exposes Zod issue JSON as Error.message. Keep that public
// boundary, including issue order and paths, without running JS at runtime.
func schemaError(issues []protocol.JSONObject) error {
	data, _ := json.Marshal(issues)
	data, _ = protocol.ParseAndStringifyJavaScriptJSON(data)
	var out bytes.Buffer
	_ = json.Indent(&out, data, "", "  ")
	return errors.New(out.String())
}
func jsonType(value json.RawMessage) string {
	if len(value) == 0 {
		return "undefined"
	}
	var v any
	if json.Unmarshal(value, &v) != nil {
		return "undefined"
	}
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}
func invalidType(expected string, value json.RawMessage, path []any) protocol.JSONObject {
	return record(field("expected", expected), field("code", "invalid_type"), field("path", path), field("message", "Invalid input: expected "+expected+", received "+jsonType(value)))
}
func fieldIssues(v json.RawMessage, typ string, path []any) []protocol.JSONObject {
	expected := typ
	if typ == "url" || typ == "safeurl" {
		expected = "string"
	}
	if typ == "strings" || typ == "safeurls" {
		expected = "array"
	}
	if typ == "bool" {
		expected = "boolean"
	}
	if jsonType(v) != expected {
		return []protocol.JSONObject{invalidType(expected, v, path)}
	}
	if typ == "strings" || typ == "safeurls" {
		var values []json.RawMessage
		_ = json.Unmarshal(v, &values)
		issues := []protocol.JSONObject{}
		for i, item := range values {
			nested := append(append([]any{}, path...), i)
			itemType := "string"
			if typ == "safeurls" {
				itemType = "safeurl"
			}
			issues = append(issues, fieldIssues(item, itemType, nested)...)
		}
		return issues
	}
	if typ == "url" || typ == "safeurl" {
		text := object(raw(map[string]any{"value": json.RawMessage(v)})).String("value")
		u, err := parseURL(text)
		if err != nil {
			issues := []protocol.JSONObject{record(field("code", "invalid_format"), field("format", "url"), field("path", path), field("message", "Invalid URL"))}
			if typ == "safeurl" {
				issues = append(issues, record(field("code", "custom"), field("message", "URL must be parseable"), field("fatal", true), field("path", path)))
			}
			return issues
		}
		if typ == "safeurl" && (u.Scheme() == "javascript" || u.Scheme() == "data" || u.Scheme() == "vbscript") {
			return []protocol.JSONObject{record(field("code", "custom"), field("path", path), field("message", "URL cannot use javascript:, data:, or vbscript: scheme"))}
		}
	}
	return nil
}
func tokenIssues(value protocol.JSONObject) []protocol.JSONObject {
	issues := []protocol.JSONObject{}
	for _, key := range []string{"access_token", "id_token", "token_type", "expires_in", "scope", "refresh_token"} {
		v := value.Get(key)
		if v == nil && key != "access_token" && key != "token_type" {
			continue
		}
		path := []any{key}
		if key == "expires_in" {
			if !finite(number(v)) {
				issue := record(field("expected", "number"), field("code", "invalid_type"), field("received", "NaN"), field("path", path), field("message", "Invalid input: expected number, received NaN"))
				if math.IsInf(number(v), 0) {
					label := "Infinity"
					if number(v) < 0 {
						label = "-Infinity"
					}
					issue.Set("received", quote(label))
					issue.Set("message", quote("Invalid input: expected number, received number"))
				}
				issues = append(issues, issue)
			}
		} else {
			issues = append(issues, fieldIssues(v, "string", path)...)
		}
	}
	return issues
}
func metadataOrder(kind string) []string {
	text := "issuer authorization_endpoint token_endpoint registration_endpoint scopes_supported response_types_supported response_modes_supported grant_types_supported token_endpoint_auth_methods_supported token_endpoint_auth_signing_alg_values_supported service_documentation revocation_endpoint revocation_endpoint_auth_methods_supported revocation_endpoint_auth_signing_alg_values_supported introspection_endpoint introspection_endpoint_auth_methods_supported introspection_endpoint_auth_signing_alg_values_supported code_challenge_methods_supported client_id_metadata_document_supported"
	if kind == "resource" {
		text = "resource authorization_servers jwks_uri scopes_supported bearer_methods_supported resource_signing_alg_values_supported resource_name resource_documentation resource_policy_uri resource_tos_uri tls_client_certificate_bound_access_tokens authorization_details_types_supported dpop_signing_alg_values_supported dpop_bound_access_tokens_required"
	}
	if kind == "oidc" {
		text = "issuer authorization_endpoint token_endpoint userinfo_endpoint jwks_uri registration_endpoint scopes_supported response_types_supported response_modes_supported grant_types_supported acr_values_supported subject_types_supported id_token_signing_alg_values_supported id_token_encryption_alg_values_supported id_token_encryption_enc_values_supported userinfo_signing_alg_values_supported userinfo_encryption_alg_values_supported userinfo_encryption_enc_values_supported request_object_signing_alg_values_supported request_object_encryption_alg_values_supported request_object_encryption_enc_values_supported token_endpoint_auth_methods_supported token_endpoint_auth_signing_alg_values_supported display_values_supported claim_types_supported claims_supported service_documentation claims_locales_supported ui_locales_supported claims_parameter_supported request_parameter_supported request_uri_parameter_supported require_request_uri_registration op_policy_uri op_tos_uri client_id_metadata_document_supported code_challenge_methods_supported"
	}
	return strings.Fields(text)
}
func metadataIssues(value protocol.JSONObject, kind string, fields []metadataField) []protocol.JSONObject {
	issues := []protocol.JSONObject{}
	byName := map[string]metadataField{}
	for _, f := range fields {
		byName[f.name] = f
	}
	for _, name := range metadataOrder(kind) {
		f, ok := byName[name]
		if !ok {
			panic(fmt.Sprintf("metadata field missing %s", name))
		}
		v := value.Get(name)
		if v == nil && !f.required {
			continue
		}
		issues = append(issues, fieldIssues(v, f.kind, []any{name})...)
	}
	return issues
}

func validateClientInfo(value protocol.JSONObject) (protocol.JSONObject, error) {
	fields := []metadataField{{"redirect_uris", "safeurls", true}, {"token_endpoint_auth_method", "string", false}, {"grant_types", "strings", false}, {"response_types", "strings", false}, {"client_name", "string", false}, {"client_uri", "safeurl", false}, {"logo_uri", "safeurl", false}, {"scope", "string", false}, {"contacts", "strings", false}, {"tos_uri", "safeurl", false}, {"policy_uri", "string", false}, {"jwks_uri", "safeurl", false}, {"jwks", "any", false}, {"software_id", "string", false}, {"software_version", "string", false}, {"software_statement", "string", false}, {"client_id", "string", true}, {"client_secret", "string", false}, {"client_id_issued_at", "number", false}, {"client_secret_expires_at", "number", false}}
	issues := []protocol.JSONObject{}
	result := protocol.NewJSONObject()
	for _, f := range fields {
		v := value.Get(f.name)
		if v == nil && !f.required {
			continue
		}
		if (f.name == "logo_uri" || f.name == "tos_uri") && string(v) == `""` {
			continue
		}
		if f.kind != "any" {
			issues = append(issues, fieldIssues(v, f.kind, []any{f.name})...)
		}
		result.Set(f.name, v)
	}
	if len(issues) > 0 {
		return protocol.JSONObject{}, schemaError(issues)
	}
	return result, nil
}
