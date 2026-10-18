package pluginsapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

// FlaggedRegistry is the process-local pluginFlagging module cache. Writes use
// the existing Source JSON shape; only an awaited load populates an empty cache.
type FlaggedRegistry struct {
	mu        sync.Mutex
	directory string
	loaded    bool
	value     protocol.JSONObject
}

func NewFlaggedRegistry(pluginsDir string) *FlaggedRegistry {
	return &FlaggedRegistry{directory: pluginsDir}
}

func (r *FlaggedRegistry) read() protocol.JSONObject {
	result := rec()
	data, err := os.ReadFile(filepath.Join(r.directory, "flagged-plugins.json"))
	if err != nil {
		return result
	}
	var root protocol.JSONObject
	if root.UnmarshalJSON(protocol.DecodeJavaScriptUTF8(data)) != nil || !root.IsObject() {
		return result
	}
	var entries protocol.JSONObject
	_ = entries.UnmarshalJSON(root.Get("plugins"))
	properties := entries.OwnProperties()
	if !entries.IsObject() {
		var rows []json.RawMessage
		if json.Unmarshal(root.Get("plugins"), &rows) != nil {
			return result
		}
		for i, row := range rows {
			properties = append(properties, protocol.JSONProperty{NameJSON: raw(strconv.Itoa(i)), Value: row})
		}
	}
	for _, property := range properties {
		name, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
		if string(protocol.QuoteJavaScriptUTF16(name)) == `"__proto__"` {
			continue
		}
		entry := obj(property.Value)
		flagged, err := protocol.DecodeJavaScriptJSONString(entry.Get("flaggedAt"))
		if err != nil {
			continue
		}
		parsed := protocol.NewJSONObject(protocol.JSONField{Name: "flaggedAt", Value: protocol.QuoteJavaScriptUTF16(flagged)})
		if seen, err := protocol.DecodeJavaScriptJSONString(entry.Get("seenAt")); err == nil {
			parsed.Set("seenAt", protocol.QuoteJavaScriptUTF16(seen))
		}
		_ = result.SetPropertyJSON(property.NameJSON, raw(parsed))
	}
	return result
}

// Source catches write failures. Cache replacement happens only after rename;
// callers with an earlier cache assignment (Load/Remove) retain that assignment.
func (r *FlaggedRegistry) write(ctx context.Context, value protocol.JSONObject) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	path := filepath.Join(r.directory, "flagged-plugins.json")
	temporary := path + "." + hex.EncodeToString(random) + ".tmp"
	defer os.Remove(temporary)
	if os.MkdirAll(r.directory, 0777) != nil {
		return nil
	}
	encoded, _ := rec(field("plugins", value)).MarshalJSON()
	var pretty bytes.Buffer
	if json.Indent(&pretty, encoded, "", "  ") != nil {
		return nil
	}
	if err := os.WriteFile(temporary, pretty.Bytes(), 0600); err != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if os.Rename(temporary, path) != nil {
		return nil
	}
	r.value = value.Clone()
	r.loaded = true
	return nil
}

func (r *FlaggedRegistry) Load(ctx context.Context, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	value := r.read()
	changed := false
	for _, property := range value.OwnProperties() {
		seen, err := protocol.DecodeJavaScriptJSONString(obj(property.Value).Get("seenAt"))
		if err != nil || len(seen) == 0 {
			continue
		}
		stamp, valid, _ := sessionprojection.ParseDateUTC(seen)
		if valid && float64(now.UnixMilli())-stamp >= 48*60*60*1000 {
			value.DeletePropertyJSON(property.NameJSON)
			changed = true
		}
	}
	r.value = value
	r.loaded = true
	if changed {
		return r.write(ctx, value)
	}
	return nil
}

func (r *FlaggedRegistry) Snapshot() protocol.JSONObject {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.loaded {
		return rec()
	}
	return r.value.Clone()
}

func (r *FlaggedRegistry) Add(ctx context.Context, id string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.loaded {
		r.value = r.read()
		r.loaded = true
	}
	value := r.value.Clone()
	value.Set(id, raw(rec(field("flaggedAt", now.UTC().Format("2006-01-02T15:04:05.000Z")))))
	return r.write(ctx, value)
}

func (r *FlaggedRegistry) MarkSeen(ctx context.Context, ids []string, now time.Time) error {
	encoded := make([]json.RawMessage, len(ids))
	for i, id := range ids {
		encoded[i] = raw(id)
	}
	return r.MarkSeenJSON(ctx, encoded, now)
}

func (r *FlaggedRegistry) MarkSeenJSON(ctx context.Context, ids []json.RawMessage, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.loaded {
		r.value = r.read()
		r.loaded = true
	}
	value := r.value.Clone()
	changed := false
	for _, id := range ids {
		if _, err := protocol.DecodeJavaScriptJSONString(id); err != nil {
			return err
		}
		encoded, _ := value.GetPropertyJSON(id)
		entry := obj(encoded)
		seen, _ := protocol.DecodeJavaScriptJSONString(entry.Get("seenAt"))
		if len(entry.OwnProperties()) == 0 || len(seen) > 0 {
			continue
		}
		entry.Set("seenAt", raw(now.UTC().Format("2006-01-02T15:04:05.000Z")))
		_ = value.SetPropertyJSON(id, raw(entry))
		changed = true
	}
	if changed {
		return r.write(ctx, value)
	}
	return nil
}

func (r *FlaggedRegistry) Remove(ctx context.Context, id string) error {
	return r.RemoveJSON(ctx, raw(id))
}

func (r *FlaggedRegistry) RemoveJSON(ctx context.Context, id json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.loaded {
		r.value = r.read()
		r.loaded = true
	}
	if _, err := protocol.DecodeJavaScriptJSONString(id); err != nil {
		return err
	}
	present, _ := r.value.GetPropertyJSON(id)
	if present == nil {
		return nil
	}
	value := r.value.Clone()
	value.DeletePropertyJSON(id)
	r.value = value
	return r.write(ctx, value)
}
