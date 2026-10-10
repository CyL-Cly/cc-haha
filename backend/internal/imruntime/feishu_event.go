package imruntime

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
	"sync"
)

type FeishuEventHandler func(context.Context, protocol.JSONObject) (json.RawMessage, error)
type FeishuDispatcher struct {
	EncryptKey, VerificationToken Text
	mu                            sync.RWMutex
	handlers                      map[string]FeishuEventHandler
}

func NewFeishuDispatcher(encryptKey, verificationToken Text) *FeishuDispatcher {
	return &FeishuDispatcher{EncryptKey: encryptKey.Clone(), VerificationToken: verificationToken.Clone(), handlers: map[string]FeishuEventHandler{}}
}
func (d *FeishuDispatcher) Register(name string, handler FeishuEventHandler) {
	d.mu.Lock()
	d.handlers[name] = handler
	d.mu.Unlock()
}
func FeishuDecryptEvent(encrypted, encryptKey Text) (json.RawMessage, error) {
	p := nodeBase64(encrypted.String())
	if len(p) < 16 || (len(p)-16)%16 != 0 {
		return nil, errors.New("wrong final block length")
	}
	key := sha256.Sum256([]byte(encryptKey.String()))
	block, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	plain := append([]byte(nil), p[16:]...)
	if len(plain) == 0 {
		return nil, errors.New("bad decrypt")
	}
	cipher.NewCBCDecrypter(block, p[:16]).CryptBlocks(plain, plain)
	pad := int(plain[len(plain)-1])
	if pad == 0 || pad > 16 || pad > len(plain) {
		return nil, errors.New("bad decrypt")
	}
	for _, v := range plain[len(plain)-pad:] {
		if int(v) != pad {
			return nil, errors.New("bad decrypt")
		}
	}
	plain = protocol.DecodeJavaScriptUTF8(plain[:len(plain)-pad])
	if !json.Valid(plain) {
		return nil, errors.New("invalid encrypted JSON")
	}
	return plain, nil
}
func feishuAssign(dst *protocol.JSONObject, v json.RawMessage) {
	if isString(v) {
		for i, unit := range TextJSON(v) {
			dst.Set(fmt.Sprint(i), raw(Text{unit}))
		}
		return
	}
	if len(v) > 0 && v[0] == '[' {
		var a []json.RawMessage
		if json.Unmarshal(v, &a) == nil {
			for i, value := range a {
				dst.Set(fmt.Sprint(i), value)
			}
		}
		return
	}
	src := object(v)
	for _, p := range src.OwnProperties() {
		_ = dst.SetPropertyJSON(p.NameJSON, p.Value)
	}
}
func FeishuParseEvent(body json.RawMessage, key Text) (string, protocol.JSONObject) {
	outer := object(body)
	rest := outer.Clone()
	rest.Delete("encrypt")
	target := rest
	if credentialTruthy(outer.Get("encrypt")) {
		p, e := FeishuDecryptEvent(TextJSON(outer.Get("encrypt")), key)
		if e != nil {
			target = protocol.NewJSONObject()
		} else {
			target = object(p)
			target.Assign(rest)
		}
	}
	hasSchema := false
	for _, p := range target.OwnProperties() {
		if bytes.Equal(p.NameJSON, raw("schema")) {
			hasSchema = true
		}
	}
	result := protocol.NewJSONObject()
	var kind json.RawMessage
	if hasSchema {
		header, event := target.Get("header"), target.Get("event")
		kind = object(header).Get("event_type")
		target.Delete("header")
		target.Delete("event")
		result.Assign(target)
		feishuAssign(&result, header)
		feishuAssign(&result, event)
	} else {
		event := target.Get("event")
		kind = object(event).Get("type")
		target.Delete("event")
		feishuAssign(&result, event)
		result.Assign(target)
	}
	name := "undefined"
	if len(kind) > 0 {
		name = jsString(kind)
	}
	return name, result
}
func FeishuEventSignatureWithHeaders(body json.RawMessage, headers protocol.JSONObject, encryptKey Text) (string, error) {
	if !headers.IsObject() {
		return "", errors.New("headers is undefined")
	}
	// Preserve JavaScript '+' (numeric timestamps/nonces are not individually
	// stringified) and stringify only the own request-body properties.
	vm := goja.New()
	headerJSON := string(raw(headers))
	bodyJSON := string(protocol.StringifyValidatedJavaScriptJSON(body))
	value, e := vm.RunString("(()=>{const h=JSON.parse(" + string(raw(headerJSON)) + "),d=JSON.parse(" + string(raw(bodyJSON)) + ");return h['x-lark-request-timestamp']+h['x-lark-request-nonce']+" + string(raw(encryptKey)) + "+JSON.stringify(d)})()")
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256([]byte(value.String()))
	return hex.EncodeToString(hash[:]), nil
}
func FeishuEventSignature(body json.RawMessage, encryptKey Text) (string, error) {
	return FeishuEventSignatureWithHeaders(body, object(object(body).Get("headers")), encryptKey)
}
func (d *FeishuDispatcher) InvokeHTTP(ctx context.Context, body json.RawMessage, headers protocol.JSONObject) (json.RawMessage, error) {
	actual := object(body).Get("headers")
	if len(actual) > 0 {
		headers = object(actual)
	}
	if len(d.EncryptKey) > 0 {
		computed, e := FeishuEventSignatureWithHeaders(body, headers, d.EncryptKey)
		if e != nil {
			return nil, e
		}
		if computed != headers.String("x-lark-signature") {
			return nil, nil
		}
	}
	return d.Invoke(ctx, body, false)
}
func (d *FeishuDispatcher) Invoke(ctx context.Context, body json.RawMessage, needCheck bool) (json.RawMessage, error) {
	if needCheck && len(d.EncryptKey) > 0 {
		computed, e := FeishuEventSignature(body, d.EncryptKey)
		if e != nil {
			return nil, e
		}
		if computed != object(object(body).Get("headers")).String("x-lark-signature") {
			return nil, nil
		}
	}
	name, event := FeishuParseEvent(body, d.EncryptKey)
	d.mu.RLock()
	h, ok := d.handlers[name]
	d.mu.RUnlock()
	if ok {
		return h(ctx, event)
	}
	if name == "app_ticket" {
		return nil, nil
	}
	return raw(fmt.Sprintf("no %s event handle", name)), nil
}
