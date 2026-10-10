package sessioncollaboration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type admission struct {
	token    *struct{ value byte }
	previous protocol.JSONObject
}
type Service struct {
	o              Options
	mu             sync.Mutex
	store          protocol.JSONObject
	ready          chan struct{}
	loadError      error
	wake           chan struct{}
	userInputs     map[string]uint64
	userAdmissions map[string]admission
	pumping        bool
	closed         bool
}

func New(o Options) (*Service, error) {
	if !filepath.IsAbs(o.StatePath) {
		return nil, errors.New("session collaboration requires an absolute state path")
	}
	if e := required(o); e != nil {
		return nil, e
	}
	if o.Files == nil {
		o.Files = OSFiles{}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.UUID == nil {
		o.UUID = uuid
	}
	s := &Service{o: o, store: object("version", 1, "revision", 0, "members", object(), "messages", []protocol.JSONObject{}), ready: make(chan struct{}), wake: make(chan struct{}), userInputs: map[string]uint64{}, userAdmissions: map[string]admission{}}
	go func() {
		b, e := o.Files.ReadFile(o.StatePath)
		if errors.Is(e, os.ErrNotExist) {
			e = nil
		} else if e == nil {
			var value protocol.JSONObject
			e = value.UnmarshalJSON(b)
			if e == nil {
				s.store, e = Migrate(value)
			}
		}
		s.loadError = e
		close(s.ready)
	}()
	return s, nil
}

// Migrate preserves unknown fields from the versionless pre-release store.
func Migrate(value protocol.JSONObject) (protocol.JSONObject, error) {
	if !value.IsObject() {
		return protocol.JSONObject{}, errors.New("Invalid collaboration state")
	}
	if v := value.Get("version"); v != nil && string(v) != "1" {
		return protocol.JSONObject{}, errors.New("Unsupported collaboration state version")
	}
	members := parsed(value.Get("members"))
	var messages []protocol.JSONObject
	if !members.IsObject() || len(value.Get("messages")) == 0 || value.Get("messages")[0] != '[' || json.Unmarshal(value.Get("messages"), &messages) != nil {
		return protocol.JSONObject{}, errors.New("Invalid collaboration state records")
	}
	for _, f := range members.OwnProperties() {
		m := parsed(f.Value)
		if !m.IsObject() || !stringEqual(m.Get("sessionId"), f.NameJSON) || !stringField(m, "rootSessionId") || !stateAllowed(m.String("state")) || m.Get("title") != nil && !stringField(m, "title") {
			return protocol.JSONObject{}, errors.New("Invalid collaboration member")
		}
	}
	for _, m := range messages {
		if !m.IsObject() || !stringField(m, "id") || !stringField(m, "sourceSessionId") || !stringField(m, "targetSessionId") || !stringField(m, "content") || !messageStatusAllowed(m.String("status")) {
			return protocol.JSONObject{}, errors.New("Invalid collaboration message")
		}
	}
	out := clone(value)
	revision := num(value.Get("revision"))
	if revision < 0 {
		revision = 0
	}
	if len(messages) > 0 && revision < 1 {
		revision = 1
	}
	out.Set("version", raw(1))
	out.Set("revision", raw(revision))
	for i := range messages {
		if messages[i].Get("revision") == nil || string(messages[i].Get("revision")) == "null" {
			messages[i].Set("revision", raw(revision))
		}
	}
	out.Set("members", raw(members))
	out.Set("messages", raw(messages))
	return out, nil
}

func (s *Service) await(ctx context.Context) error {
	if e := awaitContext(ctx, s.ready); e != nil {
		return e
	}
	return s.loadError
}
func (s *Service) notifyLocked() { close(s.wake); s.wake = make(chan struct{}) }
func (s *Service) mutate(ctx context.Context, fn func() (protocol.JSONObject, error)) (protocol.JSONObject, error) {
	if e := s.await(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return protocol.JSONObject{}, context.Canceled
	}
	previous := clone(s.store)
	priorAdmissions := map[string]admission{}
	for k, v := range s.userAdmissions {
		priorAdmissions[k] = v
	}
	result, e := fn()
	if e != nil {
		s.store = previous
		s.userAdmissions = priorAdmissions
		return protocol.JSONObject{}, e
	}
	before, _ := previous.MarshalJSON()
	after, _ := s.store.MarshalJSON()
	if string(before) == string(after) {
		return clone(result), nil
	}
	revision := num(s.store.Get("revision")) + 1
	s.store.Set("revision", raw(revision))
	oldMessages := map[string]string{}
	for _, m := range list(previous.Get("messages")) {
		b, _ := m.MarshalJSON()
		oldMessages[m.String("id")] = string(b)
	}
	messages := list(s.store.Get("messages"))
	for i := range messages {
		b, _ := messages[i].MarshalJSON()
		if oldMessages[messages[i].String("id")] != string(b) {
			messages[i].Set("revision", raw(revision))
		}
	}
	s.store.Set("messages", raw(messages))
	// Source returns references from fn, then stamps their revisions before
	// structuredClone(result). Keep those same message identities in the result.
	for _, message := range messages {
		if result.Get("id") != nil && result.String("id") == message.String("id") {
			result = clone(message)
			break
		}
		if nested := parsed(result.Get("message")); nested.IsObject() && nested.String("id") == message.String("id") {
			result.Set("message", raw(message))
			break
		}
	}
	data, _ := s.store.MarshalJSON()
	temporary := s.o.StatePath + "." + s.o.UUID() + ".tmp"
	if e = s.o.Files.MkdirAll(filepath.Dir(s.o.StatePath), 0755); e == nil {
		e = s.o.Files.WriteFile(temporary, data, 0600)
	}
	if e == nil {
		e = s.o.Files.Rename(temporary, s.o.StatePath)
	}
	if e != nil {
		s.store = previous
		s.userAdmissions = priorAdmissions
		return protocol.JSONObject{}, e
	}
	s.notifyLocked()
	return clone(result), nil
}
func (s *Service) member(id, root string, parent json.RawMessage) protocol.JSONObject {
	members := parsed(s.store.Get("members"))
	m := parsed(members.Get(id))
	if m.IsObject() {
		return m
	}
	if root == "" {
		root = id
	}
	if parent == nil {
		parent = raw(nil)
	}
	m = object("sessionId", id, "rootSessionId", root, "parentSessionId", parent, "state", "idle", "stopped", false)
	s.putMember(m)
	return m
}
func (s *Service) putMember(m protocol.JSONObject) {
	members := parsed(s.store.Get("members"))
	members.Set(m.String("sessionId"), raw(m))
	s.store.Set("members", raw(members))
}
func (s *Service) append(source, target string, content json.RawMessage, kind, id string) (protocol.JSONObject, error) {
	if id == "" {
		id = s.o.UUID()
	}
	messages := list(s.store.Get("messages"))
	for _, m := range messages {
		if m.String("id") == id {
			if m.String("sourceSessionId") != source || m.String("targetSessionId") != target || !stringEqual(m.Get("content"), content) || m.String("kind") != kind {
				return protocol.JSONObject{}, conflict("Message id was already used for different content")
			}
			return m, nil
		}
	}
	m := object("id", id, "sourceSessionId", source, "targetSessionId", target, "content", content, "kind", kind, "status", "queued", "createdAt", s.o.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	s.store.Set("messages", raw(append(messages, m)))
	return m, nil
}
func (s *Service) putMessage(m protocol.JSONObject) {
	messages := list(s.store.Get("messages"))
	for i := range messages {
		if messages[i].String("id") == m.String("id") {
			messages[i] = m
			break
		}
	}
	s.store.Set("messages", raw(messages))
}
func (s *Service) clearError(m *protocol.JSONObject) { m.Delete("error"); m.Delete("errorCode") }
func (s *Service) Close(ctx context.Context) error {
	if e := s.await(ctx); e != nil {
		return e
	}
	s.mu.Lock()
	s.closed = true
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

func (s *Service) List(ctx context.Context, options protocol.JSONObject) (protocol.JSONObject, error) {
	if e := ctx.Err(); e != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	options = clone(options)
	limit := num(options.Get("limit"))
	if options.Get("limit") == nil {
		limit = 30
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	options.Set("limit", raw(limit))
	offset := num(options.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	options.Set("offset", raw(offset))
	r, e := s.o.List(ctx, options)
	if e == nil && ctx.Err() != nil {
		e = context.Cause(ctx)
	}
	return r, e
}
func (s *Service) Status(ctx context.Context, sessionIDs []string) (protocol.JSONObject, error) {
	if e := s.await(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	wanted := ids(sessionIDs)
	s.mu.Lock()
	store := clone(s.store)
	s.mu.Unlock()
	members := []protocol.JSONObject{}
	names := []string{}
	for _, f := range parsed(store.Get("members")).OwnProperties() {
		m := parsed(f.Value)
		if wanted == nil || wanted[m.String("sessionId")] {
			members = append(members, m)
			names = append(names, m.String("sessionId"))
		}
	}
	if s.o.Titles != nil {
		if titles, e := s.o.Titles(ctx, names); e == nil {
			for i := range members {
				title := trim(titles.String(members[i].String("sessionId")))
				if title != "" && title != "Untitled Session" {
					members[i].Set("title", raw(title))
				}
			}
		}
	}
	messages := []protocol.JSONObject{}
	for _, m := range list(store.Get("messages")) {
		if wanted == nil || wanted[m.String("targetSessionId")] || wanted[m.String("sourceSessionId")] {
			messages = append(messages, m)
		}
	}
	return object("revision", store.Get("revision"), "members", members, "messages", messages), nil
}
func (s *Service) GroupStatus(ctx context.Context, id string) (protocol.JSONObject, error) {
	snapshot, e := s.Status(ctx, nil)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	root := id
	for _, m := range list(snapshot.Get("members")) {
		if m.String("sessionId") == id {
			root = m.String("rootSessionId")
			break
		}
	}
	names := []string{}
	for _, m := range list(snapshot.Get("members")) {
		if m.String("rootSessionId") == root {
			names = append(names, m.String("sessionId"))
		}
	}
	return s.Status(ctx, names)
}
func (s *Service) Candidates(ctx context.Context, query json.RawMessage) (protocol.JSONObject, error) {
	o := object()
	copyRaw(&o, "query", query)
	r, e := s.List(ctx, o)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	if e = s.await(ctx); e != nil {
		return protocol.JSONObject{}, e
	}
	if e = ctx.Err(); e != nil {
		return protocol.JSONObject{}, context.Cause(ctx)
	}
	s.mu.Lock()
	members := parsed(s.store.Get("members"))
	s.mu.Unlock()
	out := []protocol.JSONObject{}
	for _, item := range list(r.Get("sessions")) {
		id := item.String("sessionId")
		if item.Get("sessionId") == nil || string(item.Get("sessionId")) == "null" {
			id = item.String("id")
		}
		cwd := ""
		for _, k := range []string{"cwd", "workDir", "projectPath"} {
			if v := item.Get(k); v != nil && string(v) != "null" {
				cwd = item.String(k)
				break
			}
		}
		updated := ""
		for _, k := range []string{"updatedAt", "modifiedAt"} {
			if v := item.Get(k); v != nil && string(v) != "null" {
				updated = item.String(k)
				break
			}
		}
		state := parsed(members.Get(id)).String("state")
		if state == "" {
			state = "idle"
		}
		out = append(out, object("sessionId", id, "title", item.String("title"), "cwd", cwd, "status", state, "updatedAt", updated))
	}
	return object("sessions", out), nil
}
