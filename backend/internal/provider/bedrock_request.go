package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cloudauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// BedrockConfig is the original GetClient-stage credential producer, not a
// second SDK/Harness. The host binds Credentials to its current owner scope.
// The request rewrite follows the reviewed sdk-v0.80.0 Bedrock 0.26.4 source;
// that wrapper was missing from the project's old package lock.
type BedrockConfig struct {
	Region      string
	SkipAuth    bool
	Credentials func(context.Context) (cloudauth.AWSCredentials, error)
	Now         func() time.Time
}

func (config *BedrockConfig) adapt(ctx context.Context, request *http.Request, prepared *MessagesRequest) error {
	var body protocol.JSONObject
	if err := body.UnmarshalJSON(prepared.Body); err != nil {
		return errors.New("Expected request body to be an object for post /v1/messages")
	}
	if !jsonTruthy(body.Get("anthropic_version")) {
		body.Set("anthropic_version", []byte(`"bedrock-2023-05-31"`))
	}
	// Source checks per-request options.headers, not client defaultHeaders.
	if !jsonTruthy(body.Get("anthropic_beta")) && prepared.Headers.Get("anthropic-beta") != "" {
		betas := strings.Split(prepared.Headers.Get("anthropic-beta"), ",")
		raw, _ := json.Marshal(betas)
		body.Set("anthropic_beta", raw)
	}
	path := prepared.Path
	if path == "/v1/messages" || path == "/v1/messages?beta=true" || path == "/v1/complete" {
		model, err := bedrockModelPath(body.Get("model"))
		if err != nil {
			return err
		}
		stream := jsonTruthy(body.Get("stream"))
		body.Delete("model")
		body.Delete("stream")
		path = "/model/" + model + "/invoke"
		if stream {
			path += "-with-response-stream"
		}
		// SDK buildRequest replaces options.path before baseURL resolution.
		base := strings.TrimSuffix(request.URL.String(), prepared.Path)
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		target, err := url.Parse(base + strings.TrimPrefix(path, "/"))
		if err != nil {
			return err
		}
		request.URL = target
	}
	raw, err := body.MarshalJSON()
	if err != nil {
		return err
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		return err
	}
	request.Body = ioBody(raw)
	request.ContentLength = int64(len(raw))
	request.GetBody = func() (io.ReadCloser, error) { return ioBody(raw), nil }
	if config.SkipAuth {
		return nil
	}
	if config.Region == "" {
		return errors.New("Expected `awsRegion` option to be passed to the client or the `AWS_REGION` environment variable to be present")
	}
	if config.Credentials == nil {
		return errors.New("Bedrock requires actual AWS credential provider")
	}
	credentials, err := config.Credentials(ctx)
	if err != nil {
		return err
	}
	headers := map[string]string{}
	for key, values := range request.Header {
		headers[strings.ToLower(key)] = strings.Join(values, ", ")
	}
	delete(headers, "connection")
	headers["host"] = request.URL.Hostname()
	now := time.Now()
	if config.Now != nil {
		now = config.Now()
	}
	// The reviewed wrapper does not supply URL query to its signing request.
	signed := cloudauth.Sign(cloudauth.SigningRequest{Method: request.Method, Path: request.URL.EscapedPath(), Headers: headers, Body: raw}, credentials, cloudauth.SignOptions{Region: config.Region, Service: "bedrock", Time: now})
	// Source buildHeaders([signed,request.headers]) lets existing request rows
	// override signed rows. Keep that ordering, including an explicit auth row.
	prior := request.Header.Clone()
	request.Header = http.Header{}
	for key, value := range signed.Headers {
		if key == "host" {
			request.Host = value
		} else {
			request.Header.Set(key, value)
		}
	}
	for key, values := range prior {
		if strings.EqualFold(key, "host") {
			request.Host = strings.Join(values, ", ")
		}
		request.Header.Del(key)
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	return nil
}

func bedrockModelPath(raw json.RawMessage) (string, error) {
	if raw == nil || bytes.Equal(raw, []byte("null")) {
		return "", errors.New("Bedrock model is not a valid path parameter")
	}
	quoted, err := protocol.ExactJavaScriptStringJSON(raw)
	if err != nil {
		return "", err
	}
	units, err := protocol.DecodeJavaScriptJSONString(quoted)
	if err != nil {
		return "", err
	}
	for index, unit := range units {
		if unit >= 0xd800 && unit <= 0xdbff {
			if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
				return "", errors.New("URI malformed")
			}
		} else if unit >= 0xdc00 && unit <= 0xdfff {
			if index == 0 || units[index-1] < 0xd800 || units[index-1] > 0xdbff {
				return "", errors.New("URI malformed")
			}
		}
	}
	model := string(utf16.Decode(units))
	if model == "." || model == ".." || strings.EqualFold(model, "%2e") || strings.EqualFold(model, "%2e%2e") {
		return "", errors.New("Bedrock model path contains unsafe dot segment")
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(model) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~!$&'()*+,;=:@", rune(b)) {
			out.WriteByte(b)
		} else {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String(), nil
}

func ioBody(raw []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(raw)) }
