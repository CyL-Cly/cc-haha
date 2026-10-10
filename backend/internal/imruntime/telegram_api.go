package imruntime

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/http/httpproxy"
)

type TelegramAPIOptions struct {
	Token, APIRoot string
	HTTP           *http.Client
	Environment    map[string]string
}
type TelegramAPI struct {
	token, root string
	HTTP        *http.Client
}

// Bot API uses grammY's explicit node-fetch keep-alive agent. Telegram media
// uses the separate global fetch path, which receives the adapter proxy env.
func AdapterNetworkHTTP(env map[string]string, globalFetch bool) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if globalFetch {
		value := func(lower, upper string) string {
			if env[lower] != "" {
				return env[lower]
			}
			return env[upper]
		}
		proxy := (&httpproxy.Config{HTTPProxy: value("http_proxy", "HTTP_PROXY"), HTTPSProxy: value("https_proxy", "HTTPS_PROXY"), NoProxy: value("no_proxy", "NO_PROXY")}).ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) { return proxy(request.URL) }
	}
	if path := env["NODE_EXTRA_CA_CERTS"]; path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("NODE_EXTRA_CA_CERTS contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}
	return &http.Client{Transport: transport}, nil
}
func NewTelegramAPI(options TelegramAPIOptions) (*TelegramAPI, error) {
	if options.Token == "" {
		return nil, errors.New("Missing TELEGRAM_BOT_TOKEN")
	}
	root := options.APIRoot
	if root == "" {
		root = "https://api.telegram.org"
	}
	if strings.HasSuffix(root, "/") {
		return nil, errors.New("Telegram API root must not have a trailing slash")
	}
	client := options.HTTP
	if client == nil {
		var err error
		client, err = AdapterNetworkHTTP(options.Environment, false)
		if err != nil {
			return nil, err
		}
	}
	return &TelegramAPI{options.Token, root, client}, nil
}
func (api *TelegramAPI) Call(ctx context.Context, method string, payload protocol.JSONObject) (json.RawMessage, error) {
	body, err := payload.MarshalJSON()
	if err != nil {
		return nil, err
	}
	return api.request(ctx, method, bytes.NewReader(body), "application/json")
}
func (api *TelegramAPI) request(ctx context.Context, method string, body io.Reader, contentType string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", api.root+"/bot"+api.token+"/"+method, body)
	if err != nil {
		return nil, &TelegramTransportError{Name: "HttpError", Message: "Network request for '" + method + "' failed!"}
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Connection", "keep-alive")
	response, err := api.HTTP.Do(request)
	if err != nil {
		return nil, &TelegramTransportError{Name: "HttpError", Message: "Network request for '" + method + "' failed!"}
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || !json.Valid(data) {
		return nil, &TelegramTransportError{Name: "HttpError", Message: "Network request for '" + method + "' failed!"}
	}
	result := object(protocol.DecodeJavaScriptUTF8(data))
	if truthy(result.Get("ok")) {
		return result.Get("result"), nil
	}
	var code int
	_ = json.Unmarshal(result.Get("error_code"), &code)
	description := result.String("description")
	failure := &TelegramAPIError{Code: code, Description: fmt.Sprintf("Call to '%s' failed! (%d: %s)", method, code, description), Raw: result}
	params := object(result.Get("parameters"))
	if v := params.Get("retry_after"); len(v) > 0 && v[0] != '"' {
		var seconds float64
		if json.Unmarshal(v, &seconds) == nil {
			failure.RetryAfter = &seconds
		}
	}
	return nil, failure
}
func numericChat(chat string) json.RawMessage {
	if json.Valid([]byte(chat)) && len(chat) > 0 && chat[0] != '"' {
		return []byte(chat)
	}
	return raw(chat)
}
func (api *TelegramAPI) SendOptions(ctx context.Context, chat string, text Text, options protocol.JSONObject) (int64, error) {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "chat_id", Value: numericChat(chat)}, protocol.JSONField{Name: "text", Value: raw(text)})
	payload.Assign(options)
	result, err := api.Call(ctx, "sendMessage", payload)
	if err != nil {
		return 0, err
	}
	var id int64
	_ = json.Unmarshal(object(result).Get("message_id"), &id)
	return id, nil
}
func (api *TelegramAPI) SendMessage(ctx context.Context, chat string, text Text) (int64, error) {
	return api.SendOptions(ctx, chat, text, protocol.NewJSONObject())
}
func (api *TelegramAPI) EditOptions(ctx context.Context, chat string, id int64, text Text, options protocol.JSONObject) error {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "chat_id", Value: numericChat(chat)}, protocol.JSONField{Name: "message_id", Value: raw(id)}, protocol.JSONField{Name: "text", Value: raw(text)})
	payload.Assign(options)
	_, err := api.Call(ctx, "editMessageText", payload)
	return err
}
func (api *TelegramAPI) EditMessage(ctx context.Context, chat string, id int64, text Text) error {
	return api.EditOptions(ctx, chat, id, text, protocol.NewJSONObject())
}
func (api *TelegramAPI) AnswerCallback(ctx context.Context, id, text string) error {
	payload := protocol.NewJSONObject(protocol.JSONField{Name: "callback_query_id", Value: raw(id)})
	if text != "" {
		payload.Set("text", raw(text))
	}
	_, err := api.Call(ctx, "answerCallbackQuery", payload)
	return err
}
func (api *TelegramAPI) SendPhoto(ctx context.Context, chat string, buffer []byte, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("chat_id", chat)
	_ = writer.WriteField("photo", "attach://upload")
	if caption != "" {
		_ = writer.WriteField("caption", caption)
	}
	part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="upload"; filename="photo.jpg"`}, "Content-Type": {"image/jpeg"}})
	if err != nil {
		return err
	}
	if _, err = part.Write(buffer); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	_, err = api.request(ctx, "sendPhoto", &body, writer.FormDataContentType())
	return err
}
func (api *TelegramAPI) Close() { api.HTTP.CloseIdleConnections() }

type TelegramPoller struct {
	API       *TelegramAPI
	Handler   func(context.Context, protocol.JSONObject) error
	Log       func(string)
	Sleep     func(context.Context, time.Duration) error
	mu        sync.Mutex
	last      int64
	username  string
	running   bool
	cancel    context.CancelFunc
	done      chan struct{}
	result    error
	closeOnce sync.Once
}

func sleepIM(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (p *TelegramPoller) Username() string { p.mu.Lock(); defer p.mu.Unlock(); return p.username }
func (p *TelegramPoller) setup(ctx context.Context, method string) (json.RawMessage, error) {
	delay := 50 * time.Millisecond
	for {
		result, err := p.API.Call(ctx, method, protocol.NewJSONObject())
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		retry := false
		var api *TelegramAPIError
		var transport *TelegramTransportError
		if errors.As(err, &transport) {
			retry = true
		} else if errors.As(err, &api) {
			if api.Code >= 500 {
				retry = true
			} else if api.Code == 429 {
				retry = true
				if api.RetryAfter != nil {
					if err = p.Sleep(ctx, time.Duration(*api.RetryAfter*float64(time.Second))); err != nil {
						return nil, err
					}
					delay = 50 * time.Millisecond
					continue
				}
			}
		}
		if !retry {
			return nil, err
		}
		if delay != 50*time.Millisecond {
			if err = p.Sleep(ctx, delay); err != nil {
				return nil, err
			}
		}
		delay *= 2
		if delay > 20*time.Minute {
			delay = 20 * time.Minute
		}
	}
}
func (p *TelegramPoller) Start(ctx context.Context) {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	if p.Sleep == nil {
		p.Sleep = sleepIM
	}
	ctx, p.cancel = context.WithCancel(ctx)
	p.done = make(chan struct{})
	p.mu.Unlock()
	go func() {
		err := p.run(ctx)
		p.mu.Lock()
		p.running = false
		p.result = err
		close(p.done)
		p.mu.Unlock()
	}()
}
func (p *TelegramPoller) run(ctx context.Context) error {
	setupContext, cancelSetup := context.WithCancel(ctx)
	defer cancelSetup()
	results := make(chan error, 2)
	for _, method := range []string{"getMe", "deleteWebhook"} {
		method := method
		go func() {
			result, err := p.setup(setupContext, method)
			if err == nil && method == "getMe" {
				p.mu.Lock()
				p.username = object(result).String("username")
				p.mu.Unlock()
			}
			results <- err
		}()
	}
	first := <-results
	if first != nil {
		cancelSetup()
	}
	second := <-results
	if first != nil {
		return first
	}
	if second != nil {
		return second
	}
	if p.Log != nil {
		p.Log("[Telegram] Bot is running!")
	}
	firstPoll := true
	for ctx.Err() == nil {
		p.mu.Lock()
		last := p.last
		p.mu.Unlock()
		payload := protocol.NewJSONObject(protocol.JSONField{Name: "offset", Value: raw(last + 1)}, protocol.JSONField{Name: "timeout", Value: raw(30)})
		if firstPoll {
			payload.Set("allowed_updates", []byte("[]"))
		}
		updates, err := p.API.Call(ctx, "getUpdates", payload)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			var api *TelegramAPIError
			seconds := 3.0
			if errors.As(err, &api) {
				if api.Code == 401 || api.Code == 409 {
					return err
				}
				if api.Code == 429 && api.RetryAfter != nil {
					seconds = *api.RetryAfter
				}
			}
			if err = p.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
				return nil
			}
			continue
		}
		var batch []protocol.JSONObject
		if err = json.Unmarshal(updates, &batch); err != nil {
			return err
		}
		for _, update := range batch {
			var id int64
			_ = json.Unmarshal(update.Get("update_id"), &id)
			p.mu.Lock()
			p.last = id
			p.mu.Unlock()
			if p.Handler != nil {
				if err = p.Handler(ctx, update); err != nil {
					return err
				}
			}
		}
		firstPoll = false
	}
	return nil
}
func (p *TelegramPoller) Wait() error {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()
	if done != nil {
		<-done
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result
}
func (p *TelegramPoller) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		running, cancel, done, last := p.running, p.cancel, p.done, p.last
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if running {
			ctx, cancelAck := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = p.API.Call(ctx, "getUpdates", protocol.NewJSONObject(protocol.JSONField{Name: "offset", Value: raw(last + 1)}, protocol.JSONField{Name: "limit", Value: raw(1)}))
			cancelAck()
		}
		if done != nil {
			<-done
		}
		p.API.Close()
	})
}
