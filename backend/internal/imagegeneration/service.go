package imagegeneration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storagerelocations"
)

type Request struct {
	URL, Method   string
	Headers       http.Header
	Body          []byte
	RedirectError bool
}
type Response struct {
	Status    int
	Headers   http.Header
	Body      []byte
	ReadError error
}
type Options struct {
	Files                 Files
	Fetch                 func(context.Context, Request) (Response, error)
	Environment           func() map[string]string
	ConfigHome            string
	SessionID             func() string
	NowMillis             func() int64
	UUID                  func() string
	InputRoots            func() []string
	OutputDir             string
	UserProvided          func(string) bool
	UserProvidedCheck     func(context.Context, string) (bool, error)
	PrepareInputs         func(context.Context, []string, []string) ([]PreparedImage, error)
	OpenAITokens          func(context.Context) (*protocol.JSONObject, error)
	GrokTokens            func(context.Context, bool) (*protocol.JSONObject, error)
	Download              func(context.Context, string) ([]byte, error)
	Timeout               time.Duration
	CombinedSignal        func(context.Context, time.Duration) (context.Context, func() error, error)
	IsAdministrativeError func(error) bool
}
type Service struct{ options Options }

func New(options Options) (*Service, error) {
	if options.Files == nil || options.Fetch == nil || options.Environment == nil || options.SessionID == nil || options.NowMillis == nil || options.UUID == nil || !filepath.IsAbs(options.ConfigHome) {
		return nil, errors.New("image generation requires actual files/fetch/environment/session/clock/UUID and isolated config path")
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Minute
	}
	return &Service{options}, nil
}
func safeSID(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "session"
	}
	return out.String()
}
func (s *Service) roots() []string {
	if s.options.InputRoots != nil {
		return s.options.InputRoots()
	}
	sid := s.options.SessionID()
	return []string{filepath.Join(s.options.ConfigHome, "uploads", safeSID(sid)), filepath.Join(s.options.ConfigHome, "image-cache", sid), filepath.Join(s.options.ConfigHome, "cc-haha", "generated-images", safeSID(sid))}
}
func (s *Service) Generate(parent context.Context, input protocol.JSONObject, config Config) (output Output, failure error) {
	started := s.options.NowMillis()
	var ctx context.Context
	var cleanup func() error
	if s.options.CombinedSignal != nil {
		var err error
		ctx, cleanup, err = s.options.CombinedSignal(parent, s.options.Timeout)
		if err != nil {
			return Output{}, err
		}
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, s.options.Timeout)
		cleanup = func() error { cancel(); return nil }
	}
	defer func() {
		if err := cleanup(); failure == nil && err != nil {
			failure = err
		}
	}()
	var paths []string
	_ = json.Unmarshal(input.Get("referenced_image_paths"), &paths)
	for i, path := range paths {
		mapped, err := storagerelocations.Resolve(path, s.options.ConfigHome)
		if err != nil {
			return Output{}, err
		}
		paths[i] = mapped
	}
	var images []PreparedImage
	var err error
	if s.options.PrepareInputs != nil {
		images, err = s.options.PrepareInputs(ctx, paths, s.roots())
	} else if s.options.UserProvidedCheck != nil {
		images, err = PrepareInputImagesWithAuthorization(ctx, s.options.Files, paths, s.roots(), s.options.UserProvidedCheck)
	} else {
		images, err = PrepareInputImages(ctx, s.options.Files, paths, s.roots(), s.options.UserProvided)
	}
	if err != nil {
		return Output{}, err
	}
	requestInput := input.Clone()
	requestInput.Set("model", encoded(config.Model))
	raw, err := s.request(ctx, requestInput, config, images)
	if err != nil {
		return Output{}, err
	}
	if len(raw) == 0 {
		return Output{}, errors.New("The image provider completed without returning an image.")
	}
	dir := s.options.OutputDir
	if dir == "" {
		dir = filepath.Join(s.options.ConfigHome, "cc-haha", "generated-images", safeSID(s.options.SessionID()))
	}
	// Source output mkdir/chmod/write do not consume the fetch AbortSignal.
	// Keep cancellation on provider/download work while finishing an already
	// received image through the same guarded native filesystem boundary.
	outputContext := context.WithoutCancel(ctx)
	if err = s.options.Files.MkdirAll(outputContext, dir, 0700); err != nil {
		return Output{}, err
	}
	_ = s.options.Files.Chmod(outputContext, dir, 0700)
	var count int
	_ = json.Unmarshal(input.Get("count"), &count)
	if count == 0 {
		count = 1
	}
	if len(raw) > count {
		raw = raw[:count]
	}
	finished := []GeneratedImage{}
	for i, image := range raw {
		bytes, err := s.resolveBytes(ctx, image, config)
		if err != nil {
			return Output{}, err
		}
		result, err := PersistImage(outputContext, s.options.Files, dir, bytes, i, image.RevisedPrompt, s.options.NowMillis(), s.options.UUID())
		if err != nil {
			return Output{}, err
		}
		finished = append(finished, result)
	}
	if len(finished) == 0 {
		return Output{}, errors.New("The image provider returned no usable image output.")
	}
	operation := "generate"
	if len(images) > 0 {
		operation = "edit"
	}
	return Output{Type: "image_generation_result", Operation: operation, InputImageCount: len(images), ProviderID: config.ProviderID, ProviderKind: config.Kind, Model: config.Model, Prompt: input.Get("prompt"), Images: finished, DurationMS: s.options.NowMillis() - started}, nil
}
func (s *Service) request(ctx context.Context, input protocol.JSONObject, config Config, images []PreparedImage) ([]RawImage, error) {
	var count int
	_ = json.Unmarshal(input.Get("count"), &count)
	if count == 0 {
		count = 1
	}
	result := []RawImage{}
	switch config.Kind {
	case "openai_oauth":
		if s.options.OpenAITokens == nil {
			return nil, errors.New("actual ChatGPT image OAuth token producer is not bound")
		}
		tokens, err := s.options.OpenAITokens(ctx)
		if err != nil {
			return nil, err
		}
		if tokens == nil {
			return nil, errors.New("ChatGPT OAuth is missing or expired. Reconnect ChatGPT in provider settings.")
		}
		for i := 0; i < count; i++ {
			body := ChatGPTBody(input, images, s.options.Environment())
			headers := http.Header{"Accept": []string{"text/event-stream"}, "Authorization": []string{"Bearer " + tokens.String("accessToken")}, "Content-Type": []string{"application/json"}, "Originator": []string{"codex_cli_rs"}, "User-Agent": []string{desktopoauth.OpenAICodexTokenUserAgent}}
			if tokens.String("accountId") != "" {
				headers.Set("ChatGPT-Account-Id", tokens.String("accountId"))
			}
			response, err := s.options.Fetch(ctx, Request{URL: "https://chatgpt.com/backend-api/codex/responses", Method: "POST", Headers: headers, Body: encoded(body)})
			if err != nil {
				return nil, err
			}
			if err = upstreamSuccess(response, "ChatGPT image generation", []string{tokens.String("accessToken")}); err != nil {
				return nil, err
			}
			if response.ReadError != nil {
				return nil, response.ReadError
			}
			generated := ParseChatGPTStream(string(protocol.DecodeJavaScriptUTF8(response.Body)))
			if len(generated) == 0 {
				return nil, errors.New("ChatGPT completed without returning an image.")
			}
			result = append(result, generated[0])
		}
		return result, nil
	case "grok_oauth":
		if s.options.GrokTokens == nil {
			return nil, errors.New("actual Grok image OAuth token producer is not bound")
		}
		tokens, err := s.options.GrokTokens(ctx, false)
		if err != nil {
			return nil, err
		}
		if tokens == nil {
			return nil, errors.New("Grok OAuth is missing or expired. Reconnect Grok in provider settings.")
		}
		endpoint := "https://api.x.ai/v1/images/generations"
		if len(images) > 0 {
			endpoint = "https://api.x.ai/v1/images/edits"
		}
		body := GrokBody(input, images)
		response, err := s.post(ctx, endpoint, tokens.String("accessToken"), encoded(body), "application/json")
		if err != nil {
			return nil, err
		}
		if response.Status == 401 {
			next, e := s.options.GrokTokens(ctx, true)
			if e != nil && s.options.IsAdministrativeError != nil && s.options.IsAdministrativeError(e) {
				return nil, e
			}
			if e == nil && next != nil {
				tokens = next
				response, err = s.post(ctx, endpoint, tokens.String("accessToken"), encoded(body), "application/json")
				if err != nil {
					return nil, err
				}
			}
		}
		if err = upstreamSuccess(response, "Grok image generation", []string{tokens.String("accessToken")}); err != nil {
			return nil, err
		}
		if response.ReadError != nil {
			return nil, response.ReadError
		}
		return ParseCompatible(protocol.DecodeJavaScriptUTF8(response.Body)), nil
	case "openai_images":
		if config.BaseURL == "" || config.APIKey == "" {
			return nil, errors.New("The configured image API is missing a Base URL or API key.")
		}
		operation := "generations"
		if len(images) > 0 {
			operation = "edits"
		}
		endpoint, err := ImagesURL(config.BaseURL, operation)
		if err != nil {
			return nil, err
		}
		for i := 0; i < count; i++ {
			var body []byte
			contentType := "application/json"
			fields := CompatibleBody(input)
			smart := IsAPISmart(config)
			if smart {
				fields, err = APISmartFields(input, images)
				if err != nil {
					return nil, err
				}
			}
			if len(images) > 0 {
				body, contentType, err = MultipartBody(fields, images, smart, s.options.UUID())
				if err != nil {
					return nil, err
				}
			} else {
				body = encoded(fields)
			}
			response, err := s.post(ctx, endpoint, config.APIKey, body, contentType)
			if err != nil {
				return nil, err
			}
			if err = upstreamSuccess(response, "Custom image generation", []string{config.APIKey}); err != nil {
				return nil, err
			}
			if response.ReadError != nil {
				return nil, response.ReadError
			}
			generated := ParseCompatible(protocol.DecodeJavaScriptUTF8(response.Body))
			if len(generated) == 0 {
				return nil, errors.New("Custom image generation completed without returning an image.")
			}
			result = append(result, generated[0])
		}
		return result, nil
	}
	return nil, errors.New("unsupported image generation provider")
}
func (s *Service) post(ctx context.Context, url, token string, body []byte, contentType string) (Response, error) {
	return s.options.Fetch(ctx, Request{URL: url, Method: "POST", Headers: http.Header{"Accept": []string{"application/json"}, "Authorization": []string{"Bearer " + token}, "Content-Type": []string{contentType}}, Body: body})
}

var bearerPattern = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~-]+`)
var imageKeyPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]+`)

func upstreamSuccess(response Response, label string, secrets []string) error {
	if response.Status >= 200 && response.Status < 300 {
		return nil
	}
	detail := ""
	if response.ReadError == nil {
		detail = string(protocol.DecodeJavaScriptUTF8(response.Body))
	}
	for _, secret := range secrets {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	detail = bearerPattern.ReplaceAllString(detail, "Bearer [redacted]")
	detail = imageKeyPattern.ReplaceAllString(detail, "sk-[redacted]")
	detail = strings.Join(strings.FieldsFunc(detail, protocol.IsJavaScriptWhitespace), " ")
	units := utf16.Encode([]rune(detail))
	if len(units) > 500 {
		units = units[:500]
	}
	detail = string(utf16.Decode(units))
	if detail != "" {
		detail = ": " + detail
	}
	return fmt.Errorf("%s failed with HTTP %d%s", label, response.Status, detail)
}
func (s *Service) resolveBytes(ctx context.Context, image RawImage, config Config) ([]byte, error) {
	if image.Base64 != "" {
		normalized := strings.Map(func(r rune) rune {
			if protocol.IsJavaScriptWhitespace(r) {
				return -1
			}
			return r
		}, image.Base64)
		if normalized == "" || len(normalized) > (MaxGeneratedBytes*4+2)/3+8 {
			return nil, errors.New("The generated image exceeded the 30 MB size limit.")
		}
		clean := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '-' || r == '_' || r == '=' {
				return r
			}
			return -1
		}, normalized)
		if i := strings.IndexByte(clean, '='); i >= 0 {
			clean = clean[:i]
		}
		clean = strings.NewReplacer("-", "+", "_", "/").Replace(clean)
		if len(clean)%4 == 1 {
			clean = clean[:len(clean)-1]
		}
		decoded, _ := base64.RawStdEncoding.DecodeString(clean)
		if len(decoded) == 0 || len(decoded) > MaxGeneratedBytes {
			return nil, errors.New("The generated image was empty or too large.")
		}
		return decoded, nil
	}
	if image.URL == "" {
		return nil, errors.New("The image provider returned an empty image item.")
	}
	if IsAPISmart(config) {
		if s.options.Download == nil {
			return nil, errors.New("actual public image download producer is not bound")
		}
		return s.options.Download(ctx, image.URL)
	}
	if config.Kind != "grok_oauth" {
		return nil, errors.New("The custom image API returned a URL. Configure it to return b64_json so the desktop can save the image safely.")
	}
	return s.downloadGrok(ctx, image.URL)
}
