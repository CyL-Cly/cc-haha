package imregistration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
	qrcode "github.com/skip2/go-qrcode"
)

type Error struct {
	Status        int
	Code, Message string
}

func (e *Error) Error() string { return e.Message }

// PollFailure crosses the original handler's un-awaited poll promise and is
// handled by the server's outer API boundary, rather than errorResponse.
type PollFailure struct{ Cause error }

func (e *PollFailure) Error() string { return e.Cause.Error() }
func (e *PollFailure) Unwrap() error { return e.Cause }

type Options struct {
	Client   *http.Client
	BaseURL  string
	Source   string
	Adapters *adapterconfig.Service
}

type Service struct{ options Options }

func New(o Options) (*Service, error) {
	if o.Client == nil || o.Adapters == nil {
		return nil, fmt.Errorf("registration requires actual HTTP client and adapter service")
	}
	o.BaseURL = protocol.TrimJavaScriptString(o.BaseURL)
	o.Source = protocol.TrimJavaScriptString(o.Source)
	if o.BaseURL == "" {
		o.BaseURL = "https://oapi.dingtalk.com"
	}
	if o.Source == "" {
		o.Source = "DING_DWS_CLAW"
	}
	return &Service{o}, nil
}

func encoded(v any) json.RawMessage { p, _ := json.Marshal(v); return p }
func record(fields ...protocol.JSONField) protocol.JSONObject {
	return protocol.NewJSONObject(fields...)
}
func field(name string, v any) protocol.JSONField {
	return protocol.JSONField{Name: name, Value: encoded(v)}
}
func internal(message string) error { return &Error{500, "INTERNAL_ERROR", message} }

func trimString(raw json.RawMessage) (json.RawMessage, string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = encoded("")
	}
	text, err := protocol.ExactJavaScriptStringJSON(raw)
	if err != nil {
		return nil, "", err
	}
	units, err := protocol.DecodeJavaScriptJSONString(text)
	if err != nil {
		return nil, "", err
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return protocol.QuoteJavaScriptUTF16(units), string(utf16.Decode(units)), nil
}

func duration(raw json.RawMessage, fallback float64) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback, nil
	}
	// The JSON value cannot become code: it is a quoted JSON.parse argument.
	// Reuse the existing embedded ECMAScript runtime for Number's coercion.
	quoted, _ := json.Marshal(string(raw))
	value, err := goja.New().RunString("Number(JSON.parse(" + string(quoted) + "))")
	if err != nil {
		return 0, err
	}
	n := value.ToFloat()
	if math.IsInf(n, 0) || math.IsNaN(n) || n <= 0 {
		n = fallback
	}
	return n, nil
}

func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	quoted, _ := json.Marshal(string(raw))
	v, e := goja.New().RunString("Boolean(JSON.parse(" + string(quoted) + "))")
	return e == nil && v.ToBoolean()
}

func upperString(raw json.RawMessage) (json.RawMessage, string, error) {
	quoted, err := json.Marshal(string(raw))
	if err != nil {
		return nil, "", err
	}
	value, err := goja.New().RunString("JSON.stringify(JSON.parse(" + string(quoted) + ").toUpperCase())")
	if err != nil {
		return nil, "", err
	}
	result := json.RawMessage(value.String())
	units, err := protocol.DecodeJavaScriptJSONString(result)
	if err != nil {
		return nil, "", err
	}
	return result, string(utf16.Decode(units)), nil
}

func (s *Service) post(ctx context.Context, path string, body protocol.JSONObject, action string) (protocol.JSONObject, error) {
	raw, err := body.MarshalJSON()
	if err != nil {
		return record(), err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.options.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return record(), err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.options.Client.Do(req)
	if err != nil {
		return record(), err
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(res.Body)
	data := record()
	valid := err == nil && json.Valid(raw) && data.UnmarshalJSON(raw) == nil
	code := data.Get("errcode")
	zero := false
	if len(code) > 0 && code[0] != '"' && string(code) != "false" && string(code) != "null" {
		n, e := strconv.ParseFloat(string(code), 64)
		zero = e == nil && n == 0
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || !valid || !zero {
		message := strings.TrimPrefix(res.Status, strconv.Itoa(res.StatusCode)+" ")
		if truthy(data.Get("errmsg")) {
			value, e := protocol.ExactJavaScriptStringJSON(data.Get("errmsg"))
			if e != nil {
				return record(), e
			}
			units, e := protocol.DecodeJavaScriptJSONString(value)
			if e != nil {
				return record(), e
			}
			message = string(utf16.Decode(units))
		}
		if message == "" {
			message = "unknown error"
		}
		return record(), internal("[DingTalk " + action + "] " + message)
	}
	return data, nil
}

func qrDataURL(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	q.DisableBorder = true
	bits := q.Bitmap()
	// Original display options: width220, one-module margin, black on white.
	const width = 220
	size := len(bits)
	scale := float64(width) / float64(size+2)
	img := image.NewRGBA(image.Rect(0, 0, width, width))
	for y := 0; y < width; y++ {
		for x := 0; x < width; x++ {
			bx, by := int(math.Floor(float64(x)/scale))-1, int(math.Floor(float64(y)/scale))-1
			c := color.RGBA{255, 255, 255, 255}
			if bx >= 0 && by >= 0 && bx < size && by < size && bits[by][bx] {
				c = color.RGBA{0, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

func (s *Service) Begin(ctx context.Context) (protocol.JSONObject, error) {
	init, err := s.post(ctx, "/app/registration/init", record(field("source", s.options.Source)), "init")
	if err != nil {
		return record(), err
	}
	nonce, nonceText, err := trimString(init.Get("nonce"))
	if err != nil {
		return record(), err
	}
	if nonceText == "" {
		return record(), internal("[DingTalk init] missing nonce")
	}
	begin, err := s.post(ctx, "/app/registration/begin", record(protocol.JSONField{Name: "nonce", Value: nonce}), "begin")
	if err != nil {
		return record(), err
	}
	device, deviceText, err := trimString(begin.Get("device_code"))
	if err != nil {
		return record(), err
	}
	complete, completeText, err := trimString(begin.Get("verification_uri_complete"))
	if err != nil {
		return record(), err
	}
	if deviceText == "" {
		return record(), internal("[DingTalk begin] missing device_code")
	}
	if completeText == "" {
		return record(), internal("[DingTalk begin] missing verification_uri_complete")
	}
	expires, err := duration(begin.Get("expires_in"), 7200)
	if err != nil {
		return record(), err
	}
	interval, err := duration(begin.Get("interval"), 3)
	if err != nil {
		return record(), err
	}
	result := record(protocol.JSONField{Name: "deviceCode", Value: device}, protocol.JSONField{Name: "verificationUriComplete", Value: complete}, field("expiresInSeconds", expires), field("intervalSeconds", interval))
	for _, pair := range [][2]string{{"user_code", "userCode"}, {"verification_uri", "verificationUri"}} {
		value, text, e := trimString(begin.Get(pair[0]))
		if e != nil {
			return record(), e
		}
		if text != "" {
			result.Set(pair[1], value)
		}
	}
	if qr, e := qrDataURL(completeText); e == nil {
		result.Set("qrDataUrl", encoded(qr))
	}
	return result, nil
}

func (s *Service) Poll(ctx context.Context, deviceCode string) (protocol.JSONObject, error) {
	return s.PollJSON(ctx, encoded(deviceCode))
}

// PollJSON retains the original UTF-16 string through the physical HTTP body.
// Validation of the external route's type and length precedes this call.
func (s *Service) PollJSON(ctx context.Context, deviceCode json.RawMessage) (protocol.JSONObject, error) {
	result, err := s.poll(ctx, deviceCode)
	if err != nil {
		return result, &PollFailure{err}
	}
	return result, nil
}

func (s *Service) poll(ctx context.Context, deviceCode json.RawMessage) (protocol.JSONObject, error) {
	device, deviceText, err := trimString(deviceCode)
	if err != nil {
		return record(), err
	}
	if deviceText == "" {
		return record(), &Error{400, "BAD_REQUEST", "deviceCode is required"}
	}
	poll, err := s.post(ctx, "/app/registration/poll", record(protocol.JSONField{Name: "device_code", Value: device}), "poll")
	if err != nil {
		return record(), err
	}
	statusRaw, _, err := trimString(poll.Get("status"))
	if err != nil {
		return record(), err
	}
	statusRaw, status, err := upperString(statusRaw)
	if err != nil {
		return record(), err
	}
	if status == "SUCCESS" {
		id, idText, e := trimString(poll.Get("client_id"))
		if e != nil {
			return record(), e
		}
		secret, secretText, e := trimString(poll.Get("client_secret"))
		if e != nil {
			return record(), e
		}
		if idText == "" || secretText == "" {
			return record(), internal("DingTalk authorization succeeded but credentials are missing")
		}
		patch := record(protocol.JSONField{Name: "dingtalk", Value: mustJSON(record(protocol.JSONField{Name: "clientId", Value: id}, protocol.JSONField{Name: "clientSecret", Value: secret}))})
		if e = s.options.Adapters.Update(ctx, patch); e != nil {
			return record(), e
		}
		masked, e := s.options.Adapters.MaskedJSON(ctx)
		if e != nil {
			return record(), e
		}
		return record(field("status", status), protocol.JSONField{Name: "config", Value: masked}), nil
	}
	if status == "" {
		statusRaw = encoded("UNKNOWN")
	}
	result := record(protocol.JSONField{Name: "status", Value: statusRaw})
	reason, text, e := trimString(poll.Get("fail_reason"))
	if e != nil {
		return record(), e
	}
	if text != "" {
		result.Set("failReason", reason)
	}
	return result, nil
}

func mustJSON(value protocol.JSONObject) json.RawMessage { raw, _ := value.MarshalJSON(); return raw }
