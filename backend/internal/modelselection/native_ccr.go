package modelselection

// The original built-in aliases resolve before model.ts's ant-only flag
// references. This constructor belongs only to native CCR CLI producers;
// ordinary internal model selection remains guarded. Its actual cached auth
// identity and unchanged environment still choose native provider defaults.
func NewNativeCCRRemoteAuthentication(o Options, identity MetadataIdentity) (*Selector, error) {
	selector, err := newParsing(o, true)
	if err != nil {
		return nil, err
	}
	if identity.Subscriber {
		selector.thirdParty = false
	}
	selector.anthropicIdentity = &identity
	return selector, nil
}
