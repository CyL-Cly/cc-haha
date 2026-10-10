// Package modelselection ports the direct-credential model selection policy.
// It reads only original settings and environment inputs; auth storage and
// provider transport remain the callers' responsibilities.
package modelselection

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
	"github.com/dop251/goja"
	weburl "github.com/nlnwa/whatwg-url/url"
)

type CapabilityError struct{ Boundary string }

func (e *CapabilityError) Error() string {
	return "native model selection requires unported capability: " + e.Boundary
}

type Options struct {
	Environment map[string]string
	Settings    protocol.JSONObject
	// Nil is the original undefined override; explicit JSON null selects defaults.
	Override json.RawMessage
}
type Selector struct {
	options           Options
	provider          string
	thirdParty        bool
	openAIActive      bool
	anthropicIdentity *MetadataIdentity
}

// The caller must resolve isOpenAIAuthActive from the actual cached credential
// producer before using this constructor. A configured OAuth-file path alone
// is not an authenticated identity.
func NewOpenAIAuthentication(o Options) (*Selector, error) {
	s, err := NewParsing(o)
	if err != nil {
		return nil, err
	}
	s.openAIActive = true
	return s, nil
}

func NewDirectCredentials(o Options) (*Selector, error) {
	if o.Environment == nil || !o.Settings.IsObject() {
		return nil, errors.New("model selection requires original environment and merged settings")
	}
	if o.Environment["USER_TYPE"] == "ant" {
		return nil, &CapabilityError{"internal model flag configuration"}
	}
	if envTruthy(o.Environment["CLAUDE_CODE_REMOTE"]) || o.Environment["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop" {
		return nil, &CapabilityError{"managed OAuth model selection"}
	}
	// Source disables both Anthropic and OpenAI subscription auth when a direct
	// external credential is active outside managed OAuth contexts.
	if o.Environment["ANTHROPIC_API_KEY"] == "" && o.Environment["ANTHROPIC_AUTH_TOKEN"] == "" {
		return nil, &CapabilityError{"subscription/auth identity without direct credentials"}
	}
	return NewParsing(o)
}

// Parsing command frontmatter does not open a model request or require login.
// Request selection continues through NewDirectCredentials and its auth gate.
func NewParsing(o Options) (*Selector, error) {
	if o.Environment == nil || !o.Settings.IsObject() {
		return nil, errors.New("model parsing requires original environment and merged settings")
	}
	if o.Environment["USER_TYPE"] == "ant" {
		return nil, &CapabilityError{"internal model flag configuration"}
	}
	o.Environment = cloneEnvironment(o.Environment)
	o.Settings = o.Settings.Clone()
	o.Override = append(json.RawMessage(nil), o.Override...)
	provider := "firstParty"
	for _, choice := range []struct{ env, provider string }{{"CLAUDE_CODE_USE_BEDROCK", "bedrock"}, {"CLAUDE_CODE_USE_VERTEX", "vertex"}, {"CLAUDE_CODE_USE_FOUNDRY", "foundry"}, {"CLAUDE_CODE_USE_AZURE_OPENAI", "azureOpenAI"}} {
		if envTruthy(o.Environment[choice.env]) {
			provider = choice.provider
			break
		}
	}
	firstParty := true
	if base := o.Environment["ANTHROPIC_BASE_URL"]; base != "" {
		u, err := weburl.Parse(base)
		firstParty = err == nil && u.Host() == "api.anthropic.com"
	}
	return &Selector{options: o, provider: provider, thirdParty: provider != "firstParty" || !firstParty}, nil
}
func cloneEnvironment(input map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range input {
		out[k] = v
	}
	return out
}
func envTruthy(value string) bool {
	switch strings.ToLower(string(utf16.Decode(trim(utf16.Encode([]rune(value)))))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
func quote(value string) json.RawMessage {
	return protocol.QuoteJavaScriptUTF16(utf16.Encode([]rune(value)))
}
func units(raw json.RawMessage) ([]uint16, error) { return protocol.DecodeJavaScriptJSONString(raw) }
func trim(input []uint16) []uint16 {
	white := func(c uint16) bool {
		return c >= 0x9 && c <= 0xd || c == 0x20 || c == 0xa0 || c == 0x1680 || c >= 0x2000 && c <= 0x200a || c == 0x2028 || c == 0x2029 || c == 0x202f || c == 0x205f || c == 0x3000 || c == 0xfeff
	}
	for len(input) > 0 && white(input[0]) {
		input = input[1:]
	}
	for len(input) > 0 && white(input[len(input)-1]) {
		input = input[:len(input)-1]
	}
	return input
}
func lower(input []uint16) ([]uint16, error) {
	// The language engine handles Unicode case expansion while preserving lone
	// UTF-16 units. No Source business code or host bindings are executed here.
	vm := goja.New()
	f, err := vm.RunString("(function(s){return JSON.stringify(s.toLowerCase())})")
	if err != nil {
		return nil, err
	}
	call, _ := goja.AssertFunction(f)
	var output []uint16
	appendSegment := func(segment []uint16) error {
		value, err := call(goja.Undefined(), goja.StringFromUTF16(segment))
		if err != nil {
			return err
		}
		lowered, err := units([]byte(value.String()))
		output = append(output, lowered...)
		return err
	}
	start := 0
	for at := 0; at < len(input); at++ {
		unit := input[at]
		if unit >= 0xd800 && unit <= 0xdbff && at+1 < len(input) && input[at+1] >= 0xdc00 && input[at+1] <= 0xdfff {
			at++
			continue
		}
		if unit < 0xd800 || unit > 0xdfff {
			continue
		}
		// Goja's Unicode lowering decodes isolated surrogates to replacement
		// characters. Preserve each original unit at this case boundary.
		if err := appendSegment(input[start:at]); err != nil {
			return nil, err
		}
		output = append(output, unit)
		start = at + 1
	}
	if err := appendSegment(input[start:]); err != nil {
		return nil, err
	}
	return output, nil
}
func normalized(raw json.RawMessage) ([]uint16, error) {
	v, e := units(raw)
	if e != nil {
		return nil, e
	}
	return lower(trim(v))
}
func ascii(input []uint16) string { return string(utf16.Decode(input)) }
func ends(input []uint16, suffix string) bool {
	v := utf16.Encode([]rune(suffix))
	return len(input) >= len(v) && slices.Equal(input[len(input)-len(v):], v)
}
func index(input []uint16, part string) int {
	v := utf16.Encode([]rune(part))
	for i := 0; i+len(v) <= len(input); i++ {
		if slices.Equal(input[i:i+len(v)], v) {
			return i
		}
	}
	return -1
}
func isAlias(value string) bool {
	switch value {
	case "fable", "sonnet", "opus", "haiku", "best", "fable[1m]", "sonnet[1m]", "opus[1m]", "opusplan":
		return true
	}
	return false
}
func isFamily(value string) bool {
	switch value {
	case "fable", "sonnet", "opus", "haiku":
		return true
	}
	return false
}
func (s *Selector) reverse(raw json.RawMessage) json.RawMessage {
	var object protocol.JSONObject
	if object.UnmarshalJSON(s.options.Settings.Get("modelOverrides")) != nil || !object.IsObject() {
		return raw
	}
	original, _ := units(raw)
	for _, p := range object.OwnProperties() {
		value, err := units(p.Value)
		if err == nil && slices.Equal(value, original) {
			return p.NameJSON
		}
	}
	return raw
}
func (s *Selector) defaultTier(tier string) json.RawMessage {
	if s.openAIActive {
		return quote(providerproxy.ResolveOpenAICodexModel(tier, s.options.Environment))
	}
	if value := s.options.Environment["ANTHROPIC_DEFAULT_"+strings.ToUpper(tier)+"_MODEL"]; value != "" {
		return quote(value)
	}
	key := ""
	switch tier {
	case "fable":
		if s.thirdParty {
			return s.defaultTier("opus")
		}
		key = "claude-fable-5"
	case "opus":
		key = "claude-opus-5-5"
		if s.thirdParty {
			key = "claude-opus-4-7"
		}
	case "sonnet":
		key = "claude-sonnet-5-5"
		if s.thirdParty {
			key = "claude-sonnet-4-5-20250929"
		}
	case "haiku":
		key = "claude-haiku-4-5-20251001"
	}
	value := key
	if s.provider == "bedrock" {
		switch key {
		case "claude-opus-4-7":
			value = "us.anthropic.claude-opus-4-7-v1"
		case "claude-sonnet-4-5-20250929":
			value = "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
		case "claude-haiku-4-5-20251001":
			value = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
		}
	}
	if s.provider == "vertex" {
		switch key {
		case "claude-sonnet-4-5-20250929":
			value = "claude-sonnet-4-5@20250929"
		case "claude-haiku-4-5-20251001":
			value = "claude-haiku-4-5@20251001"
		}
	}
	if s.provider == "foundry" {
		switch key {
		case "claude-sonnet-4-5-20250929":
			value = "claude-sonnet-4-5"
		case "claude-haiku-4-5-20251001":
			value = "claude-haiku-4-5"
		}
	}
	var overrides protocol.JSONObject
	if overrides.UnmarshalJSON(s.options.Settings.Get("modelOverrides")) == nil && overrides.IsObject() {
		if raw := overrides.Get(key); raw != nil {
			u, e := units(raw)
			if e == nil && len(u) > 0 {
				return raw
			}
		}
	}
	return quote(value)
}
func (s *Selector) Parse(raw json.RawMessage) (json.RawMessage, error) {
	original, err := units(raw)
	if err != nil {
		return nil, err
	}
	original = trim(original)
	n, err := lower(original)
	if err != nil {
		return nil, err
	}
	has1m := !envTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_1M_CONTEXT"]) && (index(n, "[1m]") >= 0 || ends(n, ":1m"))
	base := n
	if has1m && ends(base, "[1m]") {
		base = trim(base[:len(base)-4])
	}
	name := ascii(base)
	if name == "fable" || name == "sonnet" || name == "opus" || name == "haiku" || name == "best" || name == "opusplan" {
		tier := name
		if tier == "opusplan" {
			tier = "sonnet"
		}
		if tier == "best" {
			return s.defaultTier("fable"), nil
		}
		value, err := units(s.defaultTier(tier))
		if err != nil {
			return nil, err
		}
		if has1m {
			value = append(value, utf16.Encode([]rune("[1m]"))...)
		}
		return protocol.QuoteJavaScriptUTF16(value), nil
	}
	legacy := name == "claude-opus-4-20250514" || name == "claude-opus-4-1-20250805" || name == "claude-opus-4-0" || name == "claude-opus-4-1"
	if s.provider == "firstParty" && legacy && !envTruthy(s.options.Environment["CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP"]) {
		value, _ := units(s.defaultTier("opus"))
		if has1m {
			value = append(value, utf16.Encode([]rune("[1m]"))...)
		}
		return protocol.QuoteJavaScriptUTF16(value), nil
	}
	if has1m {
		if ends(n, "[1m]") {
			original = trim(original[:len(original)-4])
		}
		original = append(original, utf16.Encode([]rune("[1m]"))...)
	}
	return protocol.QuoteJavaScriptUTF16(original), nil
}
func (s *Selector) Allowed(raw json.RawMessage) (bool, error) {
	listRaw := s.options.Settings.Get("availableModels")
	if len(listRaw) == 0 || bytes.Equal(bytes.TrimSpace(listRaw), []byte("null")) {
		return true, nil
	}
	var list []json.RawMessage
	if err := json.Unmarshal(listRaw, &list); err != nil {
		return false, err
	}
	if len(list) == 0 {
		return false, nil
	}
	model, err := normalized(s.reverse(raw))
	if err != nil {
		return false, err
	}
	entries := make([][]uint16, len(list))
	for i, v := range list {
		entries[i], err = normalized(v)
		if err != nil {
			return false, err
		}
	}
	specific := func(family string) bool {
		for _, entry := range entries {
			if isFamily(ascii(entry)) {
				continue
			}
			if at := index(entry, family); at >= 0 {
				after := at + len(family)
				if after == len(entry) || entry[after] == '-' {
					return true
				}
			}
		}
		return false
	}
	contains := func(value []uint16) bool {
		for _, entry := range entries {
			if slices.Equal(value, entry) {
				return true
			}
		}
		return false
	}
	if contains(model) && (!isFamily(ascii(model)) || !specific(ascii(model))) {
		return true, nil
	}
	for _, entry := range entries {
		family := ascii(entry)
		if !isFamily(family) || specific(family) {
			continue
		}
		if index(model, family) >= 0 {
			return true, nil
		}
		if isAlias(ascii(model)) {
			r, e := s.Parse(protocol.QuoteJavaScriptUTF16(model))
			if e != nil {
				return false, e
			}
			rUnits, e := normalized(r)
			if e != nil {
				return false, e
			}
			if index(rUnits, family) >= 0 {
				return true, nil
			}
		}
	}
	var resolved []uint16
	if isAlias(ascii(model)) {
		r, e := s.Parse(protocol.QuoteJavaScriptUTF16(model))
		if e != nil {
			return false, e
		}
		resolved, e = lower(mustUnits(r))
		if e != nil {
			return false, e
		}
		if contains(resolved) {
			return true, nil
		}
	} else {
		resolved = model
	}
	for _, entry := range entries {
		if !isFamily(ascii(entry)) && isAlias(ascii(entry)) {
			r, e := s.Parse(protocol.QuoteJavaScriptUTF16(entry))
			if e != nil {
				return false, e
			}
			v, e := lower(mustUnits(r))
			if e != nil {
				return false, e
			}
			if slices.Equal(v, model) {
				return true, nil
			}
		}
	}
	prefix := func(prefix []uint16) bool {
		return len(resolved) >= len(prefix) && slices.Equal(resolved[:len(prefix)], prefix) && (len(resolved) == len(prefix) || resolved[len(prefix)] == '-')
	}
	for _, entry := range entries {
		if isFamily(ascii(entry)) || isAlias(ascii(entry)) {
			continue
		}
		if prefix(entry) {
			return true, nil
		}
		if !strings.HasPrefix(ascii(entry), "claude-") && prefix(append(utf16.Encode([]rune("claude-")), entry...)) {
			return true, nil
		}
	}
	return false, nil
}
func mustUnits(raw json.RawMessage) []uint16 { v, _ := units(raw); return v }

// UserSpecified retains Source's undefined/null/empty-string distinction and
// filters the original value through availableModels before runtime decisions.
func (s *Selector) UserSpecified() (json.RawMessage, error) {
	specified := s.options.Override
	if specified == nil {
		if value := s.options.Environment["ANTHROPIC_MODEL"]; value != "" {
			specified = quote(value)
		} else {
			specified = s.options.Settings.Get("model")
			if v, e := units(specified); e != nil || len(v) == 0 {
				specified = nil
			}
		}
	}
	if specified != nil && !bytes.Equal(bytes.TrimSpace(specified), []byte("null")) {
		value, e := units(specified)
		if e != nil {
			return nil, e
		}
		if len(value) == 0 {
			return append(json.RawMessage(nil), specified...), nil
		}
		allowed, e := s.Allowed(specified)
		if e != nil {
			return nil, e
		}
		if allowed {
			return append(json.RawMessage(nil), specified...), nil
		}
		return nil, nil
	}
	return append(json.RawMessage(nil), specified...), nil
}
func (s *Selector) Select() (json.RawMessage, error) {
	specified, err := s.UserSpecified()
	if err != nil {
		return nil, err
	}
	if specified != nil && !bytes.Equal(bytes.TrimSpace(specified), []byte("null")) {
		return s.Parse(specified)
	}
	return s.Parse(s.DefaultSetting())
}

// SmallFastModel is Source getSmallFastModel: explicit small-fast wins, then
// the default Haiku producer (including original modelOverrides).
func (s *Selector) SmallFastModel() string {
	if model := s.options.Environment["ANTHROPIC_SMALL_FAST_MODEL"]; model != "" {
		return model
	}
	var value string
	_ = json.Unmarshal(s.defaultTier("haiku"), &value)
	return value
}
