package modelselection

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// MetadataIdentity is the original cached auth state, read by its owning
// credential service. This API never admits transport/provider execution.
type MetadataIdentity struct {
	Subscriber                      bool
	SubscriptionType, RateLimitTier string
}

// NewAnthropicAuthentication accepts the actual owner's cached auth identity.
// Resolving a model is independent of having credentials to send a request;
// the provider client continues to validate authentication separately.
func NewAnthropicAuthentication(o Options, identity MetadataIdentity) (*Selector, error) {
	selector, err := NewParsing(o)
	if err != nil {
		return nil, err
	}
	// shouldUseThirdPartyAnthropicModelDefaults excludes authenticated Claude
	// subscribers even when their base URL points at an Anthropic proxy.
	if identity.Subscriber {
		selector.thirdParty = false
	}
	selector.anthropicIdentity = &identity
	return selector, nil
}

func SelectForMetadata(o Options, identity MetadataIdentity) (json.RawMessage, error) {
	selector, err := NewAnthropicAuthentication(o, identity)
	if err != nil {
		return nil, err
	}
	return selector.Select()
}

func (selector *Selector) anthropicDefaultSetting(identity MetadataIdentity) json.RawMessage {
	apiCustomer := selector.provider == "firstParty" && !selector.thirdParty && !identity.Subscriber
	// A subscriber on a proxy is not an API customer; a non-subscriber is one
	// only when the actual default first-party host is configured.
	if apiCustomer {
		return selector.defaultTier("opus")
	}
	if identity.SubscriptionType == "max" || identity.SubscriptionType == "team" && identity.RateLimitTier == "default_claude_max_5x" {
		raw := selector.defaultTier("opus")
		if selector.provider == "firstParty" && !envTruthy(selector.options.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) {
			units, _ := units(raw)
			units = append(units, []uint16{'[', '1', 'm', ']'}...)
			raw = protocol.QuoteJavaScriptUTF16(units)
		}
		return raw
	}
	return selector.defaultTier("sonnet")
}
