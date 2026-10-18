package clibootstrap

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type nativeAnalyticsMetadataPrefix struct{ model, betas json.RawMessage }

// Only these two values precede metadata.ts's Promise.all. Session, current
// environment and core user data are deliberately consumed by enrichment later.
func captureNativeAnalyticsMetadataPrefix(model json.RawMessage, env map[string]string, settings protocol.JSONObject, subscriber, interactive bool, event analytics.Event) (nativeAnalyticsMetadataPrefix, error) {
	var err error
	if value := event.Metadata.Get("model"); openaiauth.AuthTruthy(value) {
		model, err = protocol.JavaScriptStringJSON(value)
		if err != nil {
			return nativeAnalyticsMetadataPrefix{}, err
		}
	} else if event.Context != nil && event.Context.Get("model") != nil {
		model = event.Context.Get("model")
	}
	prefix := nativeAnalyticsMetadataPrefix{model: append(json.RawMessage(nil), model...)}
	if raw := event.Metadata.Get("betas"); len(raw) > 0 && raw[0] == '"' {
		prefix.betas = append(json.RawMessage(nil), raw...)
		return prefix, nil
	}
	var name string
	if err = json.Unmarshal(model, &name); err != nil {
		return prefix, err
	}
	betas, err := requestcontext.CachedSubscriberModelBetas(name, env, settings, subscriber)
	if err != nil {
		return prefix, err
	}
	betas = requestcontext.WithThinkingSummaryBeta(betas, requestcontext.NativeModelThinkingSummaryBeta(name, env, interactive, settings, subscriber))
	if len(betas) > 0 {
		prefix.betas = quoteMain(strings.Join(betas, ","))
	}
	return prefix, nil
}

func (m *MainCommand) nativeCommandAnalyticsPrefix(scope *cooperative.Scope, event analytics.Event) (nativeAnalyticsMetadataPrefix, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nativeAnalyticsMetadataPrefix{}, cooperative.ErrRestricted
	}
	env := m.nativeCommandEnvironment()
	settings, err := m.Settings.InitialSettings()
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	if settings == nil {
		empty := protocol.NewJSONObject()
		settings = &empty
	}
	_, tokens, _, enabled, err := m.cliAuthIdentity(context.Background(), scope, false)
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	identity := modelselection.MetadataIdentity{}
	identity.Subscriber, err = openaiauth.ClaudeSubscriber(enabled, tokens)
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	if tokens != nil {
		identity.SubscriptionType = tokens.Object.String("subscriptionType")
		identity.RateLimitTier = tokens.Object.String("rateLimitTier")
	}
	model, err := modelselection.SelectForMetadata(modelselection.Options{Environment: env, Settings: settings.Clone()}, identity)
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	return captureNativeAnalyticsMetadataPrefix(model, env, settings.Clone(), identity.Subscriber, false, event)
}
func (m *MainCommand) beginNativeCommandAnalyticsRecord(scope *cooperative.Scope, event analytics.Event) (analytics.RecordEnrichment, error) {
	prefix, err := m.nativeCommandAnalyticsPrefix(scope, event)
	if err != nil {
		return nil, err
	}
	return func(next *cooperative.Scope) (protocol.JSONObject, error) {
		return m.nativeCommandAnalyticsRecordWithPrefix(next, event, prefix)
	}, nil
}

func (m *MainPrint) nativeAnalyticsPrefix(scope *cooperative.Scope, event analytics.Event) (nativeAnalyticsMetadataPrefix, error) {
	if scope == nil || !scope.BelongsTo(m.Owner) {
		return nativeAnalyticsMetadataPrefix{}, cooperative.ErrRestricted
	}
	env := m.nativeOpenAIEnvironment()
	settings, err := m.nativeSDKInitialSettings()
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	if settings == nil {
		empty := protocol.NewJSONObject()
		settings = &empty
	}
	identity, err := m.openAIAuth.AnthropicModelIdentity(context.Background(), scope, !m.options.Interactive)
	if err != nil {
		return nativeAnalyticsMetadataPrefix{}, err
	}
	var model json.RawMessage
	if m.Host != nil {
		value, e := m.Host.InferenceModel(scope)
		if e != nil {
			return nativeAnalyticsMetadataPrefix{}, e
		}
		model = quoteMain(value)
	} else {
		model, err = modelselection.SelectForMetadata(modelselection.Options{Environment: env, Settings: settings.Clone()}, identity)
		if err != nil {
			return nativeAnalyticsMetadataPrefix{}, err
		}
	}
	return captureNativeAnalyticsMetadataPrefix(model, env, settings.Clone(), identity.Subscriber, m.options.Interactive, event)
}
func (m *MainPrint) beginNativeAnalyticsRecord(scope *cooperative.Scope, event analytics.Event) (analytics.RecordEnrichment, error) {
	prefix, err := m.nativeAnalyticsPrefix(scope, event)
	if err != nil {
		return nil, err
	}
	return func(next *cooperative.Scope) (protocol.JSONObject, error) {
		return m.nativeAnalyticsRecordWithPrefix(next, event, prefix)
	}, nil
}
func (m *MainPrint) nativeAnalyticsSessionID() string {
	if m.Controller != nil {
		return m.Controller.SessionID()
	}
	if m.state != nil {
		return m.state.SessionID()
	}
	return ""
}
