package imruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// WhatsAppAuthStore keeps the original Baileys multifile schema. Initialization
// needs the real Signal key producer; it cannot be replaced with random unsigned
// credentials. Existing linked credentials do not need that producer to load.
type WhatsAppAuthOptions struct {
	InitializeCreds func() (protocol.JSONObject, error)
	Warn            func(error)
}
type WhatsAppAuthStore struct {
	Dir    string
	mu     sync.Mutex
	creds  protocol.JSONObject
	queues sync.WaitGroup
	tail   chan struct{}
	closed bool
	warn   func(error)
}

func HasWhatsAppAuth(dir string) bool {
	_, e := os.Stat(filepath.Join(dir, "creds.json"))
	return e == nil
}
func ClearWhatsAppAuth(dir string) error { return os.RemoveAll(dir) }
func OpenWhatsAppAuthStore(dir string, o WhatsAppAuthOptions) (*WhatsAppAuthStore, error) {
	absolute, e := filepath.Abs(dir)
	if e != nil {
		return nil, e
	}
	if st, e := os.Stat(absolute); e == nil && !st.IsDir() {
		return nil, fmt.Errorf("found something that is not a directory at %s, either delete it or specify a different location", absolute)
	}
	if e = os.MkdirAll(absolute, 0700); e != nil {
		return nil, e
	}
	main, backup := filepath.Join(absolute, "creds.json"), filepath.Join(absolute, "creds.json.bak")
	if !whatsAppValidJSON(main) && whatsAppValidJSON(backup) {
		data, e := os.ReadFile(backup)
		if e != nil {
			return nil, e
		}
		if e = os.WriteFile(main, data, 0600); e != nil {
			return nil, e
		}
		if e = os.Chmod(main, 0600); e != nil {
			return nil, e
		}
	}
	s := &WhatsAppAuthStore{Dir: absolute, warn: o.Warn}
	s.tail = make(chan struct{})
	close(s.tail)
	data := s.read("creds.json")
	if truthy(data) {
		s.creds = object(data)
		if !s.creds.IsObject() {
			return nil, errors.New("WhatsApp creds shape requires object for native Signal consumer")
		}
	} else {
		if o.InitializeCreds == nil {
			return nil, errors.New("WhatsApp Signal credential initialization is not bound")
		}
		s.creds, e = o.InitializeCreds()
		if e != nil {
			return nil, e
		}
	}
	return s, nil
}
func whatsAppValidJSON(p string) bool { data, e := os.ReadFile(p); return e == nil && json.Valid(data) }
func WhatsAppAuthFilename(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "/", "__"), ":", "-")
}

var whatsAppAuthFileLocks sync.Map

func whatsAppAuthFileLock(path string) *sync.Mutex {
	actual, _ := whatsAppAuthFileLocks.LoadOrStore(path, &sync.Mutex{})
	return actual.(*sync.Mutex)
}
func (s *WhatsAppAuthStore) read(name string) json.RawMessage {
	path := filepath.Join(s.Dir, WhatsAppAuthFilename(name))
	lock := whatsAppAuthFileLock(path)
	lock.Lock()
	defer lock.Unlock()
	data, e := os.ReadFile(path)
	if e != nil || !json.Valid(data) {
		return raw(nil)
	}
	value, e := WhatsAppBufferJSON(data)
	if e != nil {
		return raw(nil)
	}
	return value
}
func (s *WhatsAppAuthStore) Credentials() protocol.JSONObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	return object(raw(s.creds))
}
func (s *WhatsAppAuthStore) UpdateCredentials(update protocol.JSONObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("WhatsApp auth store closed")
	}
	s.creds.Assign(update)
	return nil
}
func (s *WhatsAppAuthStore) QueueSaveCreds() (<-chan struct{}, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("WhatsApp auth store closed")
	}
	prior := s.tail
	next := make(chan struct{})
	s.tail = next
	s.queues.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.queues.Done()
		defer close(next)
		<-prior
		e := s.saveCreds()
		if e != nil && s.warn != nil {
			s.warn(e)
		}
	}()
	return next, nil
}
func (s *WhatsAppAuthStore) saveCreds() error {
	main, backup := filepath.Join(s.Dir, "creds.json"), filepath.Join(s.Dir, "creds.json.bak")
	if whatsAppValidJSON(main) {
		if previous, e := os.ReadFile(main); e == nil {
			if e = os.WriteFile(backup, previous, 0600); e == nil {
				_ = os.Chmod(backup, 0600)
			}
		}
	}
	lock := whatsAppAuthFileLock(main)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	data := raw(s.creds)
	s.mu.Unlock()
	normalized, e := WhatsAppBufferJSON(data)
	if e != nil {
		return e
	}
	if e = os.WriteFile(main, normalized, 0666); e != nil {
		return e
	}
	return os.Chmod(main, 0600)
}
func (s *WhatsAppAuthStore) Wait(ctx context.Context) error {
	s.mu.Lock()
	tail := s.tail
	s.mu.Unlock()
	select {
	case <-tail:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *WhatsAppAuthStore) Close() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.queues.Wait() }
func (s *WhatsAppAuthStore) GetKeys(category string, ids []string) (protocol.JSONObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return protocol.JSONObject{}, errors.New("WhatsApp auth store closed")
	}
	out := protocol.NewJSONObject()
	for _, id := range ids {
		value := s.read(category + "-" + id + ".json")
		if category == "app-state-sync-key" && truthy(value) {
			v, e := WhatsAppAppStateSyncKey(value)
			if e != nil {
				return protocol.JSONObject{}, e
			}
			value = v
		}
		out.Set(id, value)
	}
	return out, nil
}
func (s *WhatsAppAuthStore) SetKeys(categories protocol.JSONObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("WhatsApp auth store closed")
	}
	type task struct {
		path  string
		value json.RawMessage
	}
	tasks := []task{}
	for _, category := range categories.OwnProperties() {
		name := TextJSON(category.NameJSON).String()
		for _, entry := range object(category.Value).OwnProperties() {
			id := TextJSON(entry.NameJSON).String()
			tasks = append(tasks, task{filepath.Join(s.Dir, WhatsAppAuthFilename(name+"-"+id+".json")), entry.Value})
		}
	}
	errs := make(chan error, len(tasks))
	var wg sync.WaitGroup
	for _, entry := range tasks {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock := whatsAppAuthFileLock(entry.path)
			lock.Lock()
			defer lock.Unlock()
			if !truthy(entry.value) {
				_ = os.Remove(entry.path)
				return
			}
			value, e := WhatsAppBufferJSON(entry.value)
			if e == nil {
				e = os.WriteFile(entry.path, value, 0666)
			}
			if e != nil {
				errs <- e
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		return e
	}
	return nil
}

// WhatsAppBufferJSON implements the SDK reviver followed by its replacer so
// byte data has one explicit Go representation and the on-disk shape stays old.
func WhatsAppBufferJSON(value json.RawMessage) (json.RawMessage, error) {
	value = stringsBytesTrim(value)
	if !json.Valid(value) {
		return nil, errors.New("invalid WhatsApp auth JSON")
	}
	if len(value) > 0 && value[0] == '[' {
		var a []json.RawMessage
		_ = json.Unmarshal(value, &a)
		for i, v := range a {
			n, e := WhatsAppBufferJSON(v)
			if e != nil {
				return nil, e
			}
			a[i] = n
		}
		return raw(a), nil
	}
	if len(value) == 0 || value[0] != '{' {
		return protocol.ParseAndStringifyJavaScriptJSON(value)
	}
	o := object(value)
	props := o.OwnProperties()
	if o.String("type") == "Buffer" {
		data, e := WhatsAppBufferBytes(value)
		if e != nil {
			return nil, e
		}
		return whatsAppBuffer(data), nil
	}
	if len(props) > 0 {
		numeric := true
		buffer := []byte{}
		for _, f := range props {
			if _, e := whatsAppParseIntPrefix(TextJSON(f.NameJSON).String()); e != nil {
				numeric = false
				break
			}
			var n float64
			if json.Unmarshal(f.Value, &n) != nil {
				numeric = false
				break
			}
			buffer = append(buffer, byte(int64(n)))
		}
		if numeric {
			return whatsAppBuffer(buffer), nil
		}
	}
	out := protocol.NewJSONObject()
	for _, f := range props {
		v, e := WhatsAppBufferJSON(f.Value)
		if e != nil {
			return nil, e
		}
		if e = out.SetPropertyJSON(f.NameJSON, v); e != nil {
			return nil, e
		}
	}
	return raw(out), nil
}
func stringsBytesTrim(v []byte) []byte { return []byte(strings.TrimSpace(string(v))) }
func WhatsAppBufferBytes(v json.RawMessage) ([]byte, error) {
	o := object(v)
	data := o.Get("data")
	if len(data) > 0 && data[0] == '"' {
		return nodeBase64(TextJSON(data).String()), nil
	}
	var a []json.RawMessage
	if json.Unmarshal(data, &a) == nil && a != nil {
		b := make([]byte, len(a))
		for i, n := range a {
			b[i] = byte(int64(qqNumber(n)))
		}
		return b, nil
	}
	return nil, errors.New("WhatsApp Buffer data must be base64 or byte array")
}
func WhatsAppAppStateSyncKey(v json.RawMessage) (json.RawMessage, error) {
	o := object(v)
	out := protocol.NewJSONObject()
	if d := o.Get("keyData"); len(d) > 0 && string(d) != "null" {
		var b []byte
		var e error
		if d[0] == '"' {
			b = nodeBase64(TextJSON(d).String())
		} else {
			b, e = WhatsAppBufferBytes(d)
		}
		if e != nil {
			return nil, e
		}
		out.Set("keyData", raw(base64.StdEncoding.EncodeToString(b)))
	}
	if d := o.Get("fingerprint"); len(d) > 0 && string(d) != "null" {
		f := object(d)
		if !f.IsObject() {
			return nil, errors.New(".proto.Message.AppStateSyncKeyData.fingerprint: object expected")
		}
		fp := protocol.NewJSONObject()
		for _, name := range []string{"rawId", "currentIndex"} {
			if n := f.Get(name); len(n) > 0 && string(n) != "null" {
				fp.Set(name, raw(uint32(int64(qqNumber(n)))))
			}
		}
		if data := f.Get("deviceIndexes"); len(data) > 0 && string(data) != "null" {
			var a []json.RawMessage
			if json.Unmarshal(data, &a) != nil {
				return nil, errors.New(".proto.Message.AppStateSyncKeyFingerprint.deviceIndexes: array expected")
			}
			if len(a) > 0 {
				values := []uint32{}
				for _, n := range a {
					values = append(values, uint32(int64(qqNumber(n))))
				}
				fp.Set("deviceIndexes", raw(values))
			}
		}
		out.Set("fingerprint", raw(fp))
	}
	if d := o.Get("timestamp"); len(d) > 0 && string(d) != "null" {
		var n int64
		if d[0] == '"' {
			n, _ = strconv.ParseInt(TextJSON(d).String(), 10, 64)
		} else if d[0] == '{' {
			bits := object(d)
			n = int64(uint64(uint32(int64(qqNumber(bits.Get("low"))))) | uint64(uint32(int64(qqNumber(bits.Get("high")))))<<32)
		} else {
			n = int64(qqNumber(d))
		}
		out.Set("timestamp", raw(strconv.FormatInt(n, 10)))
	}
	return raw(out), nil
}

func whatsAppParseIntPrefix(text string) (int64, error) {
	text = protocol.TrimJavaScriptString(text)
	end := 0
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		end = 1
	}
	start := end
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == start {
		return 0, errors.New("NaN")
	}
	return strconv.ParseInt(text[:end], 10, 64)
}
