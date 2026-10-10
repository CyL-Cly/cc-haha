package desktopoauth

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ModelMetadataCapabilityError struct{ Boundary string }

func (e *ModelMetadataCapabilityError) Error() string {
	return "native model metadata capability is not migrated: " + e.Boundary
}

// MainLoopModelForMetadata mirrors the Server process model getter using the
// original CLI credential slot. Desktop-managed haha tokens do not substitute
// for that slot; no exchange, refresh or provider request occurs here.
func (s *Service) MainLoopModelForMetadata(ctx context.Context, settings protocol.JSONObject, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !settings.IsObject() {
		return "", errors.New("model metadata requires original settings")
	}
	if env["USER_TYPE"] == "ant" {
		return "", &ModelMetadataCapabilityError{"internal model configuration"}
	}
	thirdParty := false
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		thirdParty = thirdParty || envTruthy(env[key])
	}
	managed := envTruthy(env["CLAUDE_CODE_REMOTE"]) || env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	bare := envTruthy(env["CLAUDE_CODE_SIMPLE"])
	external := env["ANTHROPIC_AUTH_TOKEN"] != "" || truthy(settings.Get("apiKeyHelper")) || env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"] != "" || env["ANTHROPIC_API_KEY"] != ""
	if !bare && env["ANTHROPIC_API_KEY"] == "" {
		external = external || s.metadataAPIKeyFromFD(env) != ""
	}
	enabled := !bare && !thirdParty && !(external && !managed)
	if env["ANTHROPIC_UNIX_SOCKET"] != "" && !bare {
		enabled = env["CLAUDE_CODE_OAUTH_TOKEN"] != ""
	}
	var tokens *protocol.JSONObject
	var err error
	if enabled {
		tokens, err = s.options.LoadCLITokens(ctx)
		if err != nil {
			return "", err
		}
	}
	identity := modelselection.MetadataIdentity{}
	if tokens != nil {
		var scopes []string
		_ = json.Unmarshal(tokens.Get("scopes"), &scopes)
		for _, scope := range scopes {
			identity.Subscriber = identity.Subscriber || scope == "user:inference"
		}
		identity.SubscriptionType, identity.RateLimitTier = tokens.String("subscriptionType"), tokens.String("rateLimitTier")
	}
	// OpenAI's Source auth activation requires no Anthropic credential source.
	// A saved account suppressed by explicit credentials does not select models.
	openAIActive := false
	if !thirdParty {
		active, err := s.metadataOpenAIActive(ctx, settings, env)
		if err != nil {
			return "", err
		}
		openAIActive = active
	}
	options := modelselection.Options{Environment: env, Settings: settings}
	var selector *modelselection.Selector
	if openAIActive {
		selector, err = modelselection.NewOpenAIAuthentication(options)
	} else {
		selector, err = modelselection.NewAnthropicAuthentication(options, identity)
	}
	if err != nil {
		return "", err
	}
	raw, err := selector.Select()
	if err != nil {
		return "", err
	}
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return "", err
	}
	for i, unit := range units {
		if unit >= 0xd800 && unit <= 0xdbff && (i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff) || unit >= 0xdc00 && unit <= 0xdfff && (i == 0 || units[i-1] < 0xd800 || units[i-1] > 0xdbff) {
			return "", &ModelMetadataCapabilityError{"unpaired UTF-16 model"}
		}
	}
	return string(utf16.Decode(units)), nil
}
func (s *Service) metadataTokenFile(path string) *protocol.JSONObject {
	data, err := s.options.Store.FileSystem().ReadFile(path)
	if err != nil {
		return nil
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data)) != nil || !value.IsObject() {
		return nil
	}
	return &value
}
func (s *Service) metadataOpenAIActive(ctx context.Context, settings protocol.JSONObject, env map[string]string) (bool, error) {
	tokens := s.metadataCachedOpenAITokens(ctx, env)
	if tokens == nil || !truthy(tokens.Get("refreshToken")) {
		return false, nil
	}
	bare := envTruthy(env["CLAUDE_CODE_SIMPLE"])
	managed := envTruthy(env["CLAUDE_CODE_REMOTE"]) || env["CLAUDE_CODE_ENTRYPOINT"] == "claude-desktop"
	if env["ANTHROPIC_API_KEY"] != "" || !bare && truthy(settings.Get("apiKeyHelper")) {
		return false, nil
	}
	if !bare {
		if env["ANTHROPIC_AUTH_TOKEN"] != "" && !managed || env["CLAUDE_CODE_OAUTH_TOKEN"] != "" || s.metadataAPIKeyFromFD(env) != "" {
			return false, nil
		}
		cli, err := s.options.LoadCLITokens(ctx)
		if err != nil {
			return false, err
		}
		if cli != nil && truthy(cli.Get("accessToken")) {
			var scopes []string
			_ = json.Unmarshal(cli.Get("scopes"), &scopes)
			for _, scope := range scopes {
				if scope == "user:inference" {
					return false, nil
				}
			}
		}
	}
	if bare {
		return true, nil
	}
	// Source's memoized /login getter first checks the legacy Keychain slot,
	// then the original Global fallback. It does not borrow desktop haha OAuth.
	s.metadataLegacyAPIKeyOnce.Do(func() {
		key, err := s.options.Secure.ReadLegacyManagedAPIKey(ctx)
		if err == nil {
			s.metadataLegacyAPIKey = key
		}
	})
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if s.metadataLegacyAPIKey != "" {
		return false, nil
	}
	global, err := s.options.Global.Get()
	if err != nil {
		return false, err
	}
	if truthy(global.Get("primaryApiKey")) {
		return false, nil
	}
	return true, nil
}

func (s *Service) metadataAPIKeyFromFD(env map[string]string) string {
	s.metadataAPIKeyOnce.Do(func() {
		fallback := s.options.CCRAPIKeyPath
		if fallback == "" {
			fallback = "/home/claude/.claude/remote/.api_key"
		}
		read := func(path string) string {
			data, err := s.options.Store.FileSystem().ReadFile(path)
			if err != nil {
				return ""
			}
			return trimJS(string(protocol.DecodeJavaScriptUTF8(data)))
		}
		descriptor := env["CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR"]
		if descriptor == "" {
			s.metadataAPIKey = read(fallback)
			return
		}
		fd, valid := parseDescriptor(descriptor)
		if !valid {
			return
		}
		path := ""
		if s.options.APIKeyFDPath != nil {
			path = s.options.APIKeyFDPath(fd)
		} else if s.options.Platform == "darwin" || s.options.Platform == "freebsd" {
			path = "/dev/fd/" + strconv.FormatInt(fd, 10)
		} else {
			path = "/proc/self/fd/" + strconv.FormatInt(fd, 10)
		}
		s.metadataAPIKey = read(path)
		if s.metadataAPIKey == "" {
			s.metadataAPIKey = read(fallback)
		}
	})
	return s.metadataAPIKey
}

func (s *Service) metadataCachedOpenAITokens(ctx context.Context, env map[string]string) (tokens *protocol.JSONObject) {
	key := "secure-storage"
	path := trimJS(env["OPENAI_CODEX_OAUTH_FILE"])
	if path != "" {
		key = "file:" + path
	}
	s.metadataOpenAIMu.Lock()
	defer s.metadataOpenAIMu.Unlock()
	if value, ok := s.metadataOpenAICache[key]; ok {
		return value
	}
	defer func() {
		if s.metadataOpenAICache == nil {
			s.metadataOpenAICache = map[string]*protocol.JSONObject{}
		}
		s.metadataOpenAICache[key] = tokens
	}()
	if path := trimJS(env["OPENAI_CODEX_OAUTH_FILE"]); path != "" {
		tokens = s.metadataTokenFile(path)
		if tokens != nil {
			for _, key := range []string{"accessToken", "refreshToken"} {
				if _, err := protocol.DecodeJavaScriptJSONString(tokens.Get(key)); err != nil {
					return nil
				}
			}
			var expiry float64
			if string(tokens.Get("expiresAt")) == "null" || json.Unmarshal(tokens.Get("expiresAt"), &expiry) != nil {
				return nil
			}
		}
	} else {
		// The marker makes a removed file-backed account stay logged out instead
		// of reviving stale tokens from the original secure credential record.
		marker := filepath.Join(s.options.ConfigDir, "cc-haha", "openai-oauth-file-backed")
		if _, err := s.options.Store.FileSystem().ReadFile(marker); err == nil {
			return nil
		}
		stored, err := s.options.Secure.Read(ctx)
		if err != nil || stored == nil {
			return nil
		}
		value := object(stored.Get("openaiCodexOauth"))
		if value.IsObject() {
			tokens = &value
		}
	}

	return tokens
}
