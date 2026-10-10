package adapterconfig

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

// Service owns the same adapter document as Reader. Runtime credential reads
// remain unmasked; desktop views and mutations use the original mask/merge API.
type ServiceOptions struct {
	ConfigDir string
	Store     *storage.Store
	LogError  func(error)
	UUID      func() string
	Chmod     func(string, os.FileMode) error
}
type Service struct {
	options ServiceOptions
	reader  *Reader
}

var Platforms = []string{"telegram", "feishu", "wechat", "dingtalk", "whatsapp", "wecom", "qq", "slack"}
var secrets = map[string][]string{"telegram": {"botToken"}, "feishu": {"appSecret", "encryptKey", "verificationToken"}, "wechat": {"botToken"}, "dingtalk": {"clientSecret"}, "wecom": {"secret"}, "qq": {"appSecret"}, "slack": {"botToken", "appToken"}, "pairing": {"code"}}

func NewService(o ServiceOptions) (*Service, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.Store == nil || o.LogError == nil {
		return nil, errors.New("adapter service requires explicit config/shared storage/log")
	}
	if o.UUID == nil {
		o.UUID = func() string {
			var b [16]byte
			_, _ = rand.Read(b[:])
			b[6] = (b[6] & 15) | 64
			b[8] = (b[8] & 63) | 128
			h := hex.EncodeToString(b[:])
			return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
		}
	}
	reader, err := New(Options{ConfigDir: o.ConfigDir, ReadFile: o.Store.FileSystem().ReadFile, LogError: o.LogError})
	if err != nil {
		return nil, err
	}
	return &Service{o, reader}, nil
}
func adapterJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
func adapterObject(raw json.RawMessage) protocol.JSONObject {
	var object protocol.JSONObject
	_ = object.UnmarshalJSON(raw)
	return object
}
func adapterTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "false" || string(raw) == "0" || string(raw) == `""` {
		return false
	}
	return true
}
func adapterSpread(raw json.RawMessage) protocol.JSONObject {
	result := adapterObject(raw)
	if result.IsObject() {
		return result.Clone()
	}
	result = protocol.NewJSONObject()
	if len(raw) == 0 {
		return result
	}
	if raw[0] == '[' {
		var values []json.RawMessage
		_ = json.Unmarshal(raw, &values)
		for i, value := range values {
			result.Set(fmt.Sprint(i), value)
		}
	} else if raw[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err == nil {
			for i, unit := range units {
				result.Set(fmt.Sprint(i), protocol.QuoteJavaScriptUTF16([]uint16{unit}))
			}
		}
	}
	return result
}
func (s *Service) ReadJSON(ctx context.Context) (json.RawMessage, error) {
	return s.reader.ReadJSON(ctx)
}
func (s *Service) GetRawConfig(ctx context.Context) (protocol.JSONObject, error) {
	return s.reader.GetRawConfig(ctx)
}
func (s *Service) MaskedJSON(ctx context.Context) (json.RawMessage, error) {
	raw, err := s.ReadJSON(ctx)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	object := adapterObject(raw)
	if !object.IsObject() {
		return raw, nil
	}
	for _, group := range append(append([]string{}, Platforms...), "pairing") {
		section := adapterObject(object.Get(group))
		if !section.IsObject() {
			continue
		}
		for _, key := range secrets[group] {
			value := section.Get(key)
			if !adapterTruthy(value) {
				continue
			}
			units, err := protocol.DecodeJavaScriptJSONString(value)
			if err != nil {
				return nil, &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
			}
			masked := "****"
			if group == "pairing" {
				masked = "******"
			} else if len(units) > 4 {
				prefix, _ := protocol.DecodeJavaScriptJSONString(adapterJSON(masked))
				section.Set(key, protocol.QuoteJavaScriptUTF16(append(prefix, units[len(units)-4:]...)))
				continue
			}
			section.Set(key, adapterJSON(masked))
		}
		object.Set(group, adapterJSON(section))
	}
	return object.MarshalJSON()
}
func (s *Service) Update(ctx context.Context, patch protocol.JSONObject) error {
	return s.update(ctx, patch)
}

// Only the original typed QR producer supplies undefined userId. JSON cannot
// encode undefined, so delete that inherited field inside the same atomic write.
func (s *Service) UpdateWechatBinding(ctx context.Context, section protocol.JSONObject) error {
	// Source isMasked calls startsWith on truthy values before writing. The QR
	// protocol is permissive, so a non-string token must fail at that same point.
	if value := section.Get("botToken"); adapterTruthy(value) && (len(value) == 0 || value[0] != '"') {
		return &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
	}
	patch := protocol.NewJSONObject(protocol.JSONField{Name: "wechat", Value: adapterJSON(section)})
	if section.Get("userId") == nil {
		return s.update(ctx, patch, [2]string{"wechat", "userId"})
	}
	return s.update(ctx, patch)
}

func (s *Service) update(ctx context.Context, patch protocol.JSONObject, undefined ...[2]string) error {
	return s.options.Store.WithWriteLock(filepath.Join(s.options.ConfigDir, "adapters.json"), func() error {
		raw, err := s.ReadJSON(ctx)
		if err != nil {
			return err
		}
		if string(raw) == "null" {
			return &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
		current := adapterSpread(raw)
		for group, keys := range secrets {
			section := adapterObject(patch.Get(group))
			if !section.IsObject() {
				continue
			}
			old := adapterObject(current.Get(group))
			for _, key := range keys {
				var value string
				if json.Unmarshal(section.Get(key), &value) == nil && strings.HasPrefix(value, "****") {
					section.Set(key, old.Get(key))
				}
			}
			patch.Set(group, adapterJSON(section))
		}
		merged := current.Clone()
		merged.Assign(patch)
		for _, group := range append(append([]string{}, Platforms...), "pairing") {
			replacement := patch.Get(group)
			if adapterTruthy(replacement) || (group == "pairing" && replacement != nil) {
				section := adapterSpread(current.Get(group))
				section.Assign(adapterSpread(replacement))
				merged.Set(group, adapterJSON(section))
			} else {
				merged.Set(group, current.Get(group))
			}
		}
		for _, field := range undefined {
			section := adapterSpread(merged.Get(field[0]))
			section.Set(field[1], nil)
			merged.Set(field[0], adapterJSON(section))
		}
		return s.write(ctx, merged)
	})
}

// UnbindSlack reproduces updateConfig's shallow merge with undefined token
// properties: JSON serialization removes credentials while preserving the
// platform's workspace and future fields.
func (s *Service) UnbindSlack(ctx context.Context) error {
	return s.unbind(ctx, "slack", []string{"botToken", "appToken"})
}

// UnbindDingTalk removes the original account/card identities and paired users
// through the same atomic document writer, retaining workspace/future fields.
func (s *Service) UnbindDingTalk(ctx context.Context) error {
	return s.unbind(ctx, "dingtalk", []string{"clientId", "clientSecret", "permissionCardTemplateId"})
}

func (s *Service) UnbindFeishu(ctx context.Context) error {
	return s.unbind(ctx, "feishu", []string{"appId", "appSecret", "encryptKey", "verificationToken", "domain"})
}

func (s *Service) UnbindWecom(ctx context.Context) error {
	return s.unbind(ctx, "wecom", []string{"botId", "secret"})
}

func (s *Service) UnbindQQ(ctx context.Context) error {
	return s.unbind(ctx, "qq", []string{"appId", "appSecret"})
}

func (s *Service) UnbindWechat(ctx context.Context) error {
	return s.unbind(ctx, "wechat", []string{"accountId", "botToken", "userId"}, protocol.JSONField{Name: "baseUrl", Value: adapterJSON("https://ilinkai.weixin.qq.com")})
}

func (s *Service) unbind(ctx context.Context, platform string, remove []string, extra ...protocol.JSONField) error {
	return s.options.Store.WithWriteLock(filepath.Join(s.options.ConfigDir, "adapters.json"), func() error {
		data, err := s.ReadJSON(ctx)
		if err != nil {
			return err
		}
		if string(data) == "null" {
			return &Error{500, "INTERNAL_ERROR", "An unexpected error occurred"}
		}
		current := adapterSpread(data)
		section := adapterSpread(current.Get(platform))
		for _, key := range remove {
			section.Set(key, nil)
		}
		section.Set("allowedUsers", adapterJSON([]any{}))
		section.Set("pairedUsers", adapterJSON([]any{}))
		for _, field := range extra {
			section.Set(field.Name, field.Value)
		}
		current.Set(platform, adapterJSON(section))
		return s.write(ctx, current)
	})
}
func (s *Service) write(ctx context.Context, object protocol.JSONObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fs := s.options.Store.FileSystem()
	path := filepath.Join(s.options.ConfigDir, "adapters.json")
	if err := fs.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp." + s.options.UUID()
	raw, err := object.MarshalJSON()
	if err != nil {
		return err
	}
	var formatted bytes.Buffer
	if err = json.Indent(&formatted, raw, "", "  "); err != nil {
		return err
	}
	if err = fs.WriteFile(tmp, append(formatted.Bytes(), '\n'), 0600); err == nil {
		err = fs.Rename(tmp, path)
	}
	if err != nil {
		_ = fs.Remove(tmp)
		s.options.LogError(err)
		return &Error{500, "INTERNAL_ERROR", "Failed to write adapter config"}
	}
	if s.options.Chmod != nil {
		_ = s.options.Chmod(path, 0600)
	} else {
		_ = os.Chmod(path, 0600)
	}
	return nil
}
