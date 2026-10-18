package modelselection

// FirstPartyCommandAvailable is COMMANDS' claude-ai-or-console availability
// over the already resolved native auth metadata. Transport credentials remain
// owned by the provider and are never inferred from a configured key string.
func (s *Selector) FirstPartyCommandAvailable() bool {
	return s != nil && s.anthropicIdentity != nil && (s.anthropicIdentity.Subscriber || s.provider == "firstParty" && !s.thirdParty)
}

// ClaudeAISubscriberAvailable observes the same cached identity used by the
// first-party default model selection and COMMANDS' claude-ai availability.
func (s *Selector) ClaudeAISubscriberAvailable() bool {
	return s != nil && s.anthropicIdentity != nil && s.anthropicIdentity.Subscriber
}
