package cliauth

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Property struct {
	Label string
	Value json.RawMessage
}

// GeneralStatusInput contains observations from the real credential/config
// producers. No secret value is included in authStatus's output.
type GeneralStatusInput struct {
	AuthTokenSource, APIKeySource, APIProvider      string
	HasToken, HasAPIKeyEnvironment, UsingThirdParty bool
	OAuthAccount, SubscriptionType, OpenAITokens    json.RawMessage
	Properties                                      []Property
}

func GeneralStatus(in GeneralStatusInput, text bool) Result {
	usingOpenAI := truthy(rawField(in.OpenAITokens, "refreshToken")) && !in.HasToken && in.APIKeySource == "none" && !in.HasAPIKeyEnvironment && !in.UsingThirdParty
	loggedIn := in.HasToken || in.APIKeySource != "none" || in.HasAPIKeyEnvironment || in.UsingThirdParty || usingOpenAI
	method := "none"
	switch {
	case usingOpenAI:
		method = "openai_oauth"
	case in.UsingThirdParty:
		method = "third_party"
	case in.AuthTokenSource == "claude.ai":
		method = "claude.ai"
	case in.AuthTokenSource == "apiKeyHelper":
		method = "api_key_helper"
	case in.AuthTokenSource != "none":
		method = "oauth_token"
	case in.APIKeySource == "ANTHROPIC_API_KEY" || in.HasAPIKeyEnvironment:
		method = "api_key"
	case in.APIKeySource == "/login managed key":
		method = "claude.ai"
	}
	code := 1
	if loggedIn {
		code = 0
	}
	if text {
		stdout := ""
		hasAuthProperty := false
		for _, property := range in.Properties {
			value := property.Value
			if isString(value) {
				value = protocol.DecodeJavaScriptUTF8([]byte(textString(value)))
			} else if len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '[' {
				var values []json.RawMessage
				if json.Unmarshal(value, &values) != nil {
					continue
				}
				joined := ""
				for i, item := range values {
					if i > 0 {
						joined += ", "
					}
					if item != nil && string(item) != "null" {
						s, _ := protocol.ExactJavaScriptStringJSON(item)
						joined += textString(s)
					}
				}
				value = []byte(joined)
			} else {
				continue
			}
			if string(value) == "none" {
				continue
			}
			hasAuthProperty = true
			if property.Label != "" {
				stdout += property.Label + ": "
			}
			stdout += string(value) + "\n"
		}
		if !hasAuthProperty && in.HasAPIKeyEnvironment {
			stdout += "API key: ANTHROPIC_API_KEY\n"
		}
		if !loggedIn {
			stdout += "Not logged in. Run claude auth login to authenticate.\n"
		}
		return Result{Stdout: stdout, ExitCode: code}
	}
	object := protocol.NewJSONObject(protocol.JSONField{Name: "loggedIn", Value: json.RawMessage(strconv.FormatBool(loggedIn))}, protocol.JSONField{Name: "authMethod", Value: quoteStatus(method)}, protocol.JSONField{Name: "apiProvider", Value: quoteStatus(in.APIProvider)})
	source := in.APIKeySource
	if source == "none" {
		source = ""
		if in.HasAPIKeyEnvironment {
			source = "ANTHROPIC_API_KEY"
		}
	}
	if source != "" {
		object.Set("apiKeySource", quoteStatus(source))
	}
	if method == "claude.ai" {
		for _, pair := range []struct{ field, source string }{{"email", "emailAddress"}, {"orgId", "organizationUuid"}, {"orgName", "organizationName"}} {
			object.Set(pair.field, nullish(rawField(in.OAuthAccount, pair.source)))
		}
		object.Set("subscriptionType", nullish(in.SubscriptionType))
	} else if method == "openai_oauth" {
		object.Set("provider", quoteStatus("openai"))
		object.Set("email", nullish(rawField(in.OpenAITokens, "email")))
		object.Set("accountId", nullish(rawField(in.OpenAITokens, "accountId")))
		object.Set("subscriptionType", quoteStatus("ChatGPT Pro/Plus"))
	}
	raw, _ := object.MarshalJSON()
	normalized, _ := protocol.ParseAndStringifyJavaScriptJSON(raw)
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, normalized, "", "  ")
	return Result{Stdout: pretty.String() + "\n", ExitCode: code}
}

func quoteStatus(value string) json.RawMessage { raw, _ := json.Marshal(value); return raw }

// AccountProperties is the original first-party account projection consumed by
// text status. API/provider/network properties are produced by their own stage.
func AccountProperties(in GeneralStatusInput, apiKeyPresent, subscriber, demo bool) []Property {
	if in.APIProvider != "firstParty" {
		return nil
	}
	add := func(list []Property, label string, value json.RawMessage) []Property {
		if openaiauth.AuthTruthy(value) {
			list = append(list, Property{label, value})
		}
		return list
	}
	list := []Property{}
	if truthy(rawField(in.OpenAITokens, "refreshToken")) && !in.UsingThirdParty && in.AuthTokenSource == "none" && in.APIKeySource == "none" {
		list = append(list, Property{"Provider", quoteStatus("OpenAI/Codex")}, Property{"Subscription", quoteStatus("ChatGPT Pro/Plus")}, Property{"Auth token", quoteStatus("OpenAI OAuth")})
		if !demo {
			list = add(list, "Email", rawField(in.OpenAITokens, "email"))
			list = add(list, "Account ID", rawField(in.OpenAITokens, "accountId"))
		}
		return list
	}
	if in.AuthTokenSource != "CLAUDE_CODE_OAUTH_TOKEN" && in.AuthTokenSource != "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR" && subscriber {
		subscription := "Claude API"
		switch textString(in.SubscriptionType) {
		case "enterprise":
			subscription = "Claude Enterprise"
		case "team":
			subscription = "Claude Team"
		case "max":
			subscription = "Claude Max"
		case "pro":
			subscription = "Claude Pro"
		}
		list = append(list, Property{"Login method", quoteStatus(subscription + " Account")})
	} else {
		list = add(list, "Auth token", quoteStatus(in.AuthTokenSource))
	}
	if apiKeyPresent {
		list = add(list, "API key", quoteStatus(in.APIKeySource))
	}
	if !demo && (in.AuthTokenSource == "claude.ai" || in.APIKeySource == "/login managed key") {
		list = add(list, "Organization", rawField(in.OAuthAccount, "organizationName"))
		list = add(list, "Email", rawField(in.OAuthAccount, "emailAddress"))
	}
	return list
}
