package imruntime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Pairing struct {
	Lifecycle *MigrationLifecycle
	ConfigDir string
	NowMillis func() int64
	Service   *adapterconfig.Service
	mu        sync.Mutex
	failures  map[string]pairingFailure
}
type pairingFailure struct {
	count int
	first int64
}

func NewPairing(config string, service *adapterconfig.Service) *Pairing {
	return &Pairing{ConfigDir: config, Service: service, NowMillis: func() int64 { return time.Now().UnixMilli() }, failures: map[string]pairingFailure{}}
}
func (p *Pairing) read() protocol.JSONObject {
	data, err := os.ReadFile(filepath.Join(p.ConfigDir, "adapters.json"))
	if err != nil {
		return protocol.NewJSONObject()
	}
	value := object(protocol.DecodeJavaScriptUTF8(data))
	if !value.IsObject() {
		return protocol.NewJSONObject()
	}
	return value
}
func jsString(value json.RawMessage) string {
	quoted, err := protocol.ExactJavaScriptStringJSON(value)
	if err != nil {
		return ""
	}
	var text string
	_ = json.Unmarshal(quoted, &text)
	return text
}
func (p *Pairing) Allowed(platform string, user json.RawMessage) bool {
	document := p.read()
	config := object(document.Get(platform))
	var allowed, paired []json.RawMessage
	_ = json.Unmarshal(config.Get("allowedUsers"), &allowed)
	_ = json.Unmarshal(config.Get("pairedUsers"), &paired)
	for _, value := range allowed {
		if string(protocol.StringifyValidatedJavaScriptJSON(value)) == string(protocol.StringifyValidatedJavaScriptJSON(user)) {
			return true
		}
	}
	for _, value := range paired {
		if string(value) == "null" {
			return false
		}
		entry := object(value)
		if jsString(entry.Get("userId")) == jsString(user) {
			return true
		}
	}
	return false
}
func (p *Pairing) TryPair(platform, text, displayName string, user json.RawMessage) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	document := p.read()
	pairing := object(document.Get("pairing"))
	key := jsString(user)
	now := p.NowMillis()
	attempt, exists := p.failures[key]
	if exists && now-attempt.first > 300000 {
		delete(p.failures, key)
		exists = false
	}
	if exists && attempt.count >= 5 {
		return false, nil
	}
	code, expiry := pairing.Get("code"), pairing.Get("expiresAt")
	if !truthy(code) || !truthy(expiry) {
		return false, nil
	}
	var expires float64
	if json.Unmarshal(expiry, &expires) != nil {
		var err error
		expires, err = strconv.ParseFloat(jsString(expiry), 64)
		if err != nil {
			expires = math.NaN()
		}
	}
	if float64(now) > expires {
		return false, nil
	}
	var codeString string
	if json.Unmarshal(code, &codeString) != nil {
		return false, nil
	}
	if strings.ToUpper(protocol.TrimJavaScriptString(text)) != strings.ToUpper(codeString) {
		if !exists || now-attempt.first > 300000 {
			attempt = pairingFailure{1, now}
		} else {
			attempt.count++
		}
		p.failures[key] = attempt
		return false, nil
	}
	section := object(document.Get(platform))
	var users []json.RawMessage
	_ = json.Unmarshal(section.Get("pairedUsers"), &users)
	if users == nil {
		users = []json.RawMessage{}
	}
	found := false
	for _, entry := range users {
		if jsString(object(entry).Get("userId")) == key {
			found = true
		}
	}
	if !found {
		users = append(users, raw(protocol.NewJSONObject(protocol.JSONField{Name: "userId", Value: user}, protocol.JSONField{Name: "displayName", Value: raw(displayName)}, protocol.JSONField{Name: "pairedAt", Value: raw(now)})))
	}
	if !section.IsObject() {
		section = protocol.NewJSONObject()
	}
	section.Set("pairedUsers", raw(users))
	document.Set(platform, raw(section))
	document.Set("pairing", []byte(`{"code":null,"expiresAt":null,"createdAt":null}`))
	if err := p.write(document); err != nil {
		return false, err
	}
	return true, nil
}

func (p *Pairing) write(document protocol.JSONObject) (err error) {
	done := p.Lifecycle.Track()
	defer func() { done(err) }()
	path := filepath.Join(p.ConfigDir, "adapters.json")
	if _, err := os.Stat(filepath.Dir(path)); os.IsNotExist(err) {
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
	}
	var bytes [8]byte
	if _, err := io.ReadFull(rand.Reader, bytes[:]); err != nil {
		return err
	}
	tmp := path + ".tmp." + hex.EncodeToString(bytes[:])
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
