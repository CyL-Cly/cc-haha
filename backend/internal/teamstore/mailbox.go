// Package teamstore owns the original private team files and teammate inboxes.
// Task transactions and cross-process leases reuse the existing taskstore.
package teamstore

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

type MailboxOptions struct {
	ConfigDir string
	TaskStore *taskstore.Service
	TeamName  func() string
	NewID     func() (string, error)
	Log       func(error)
}
type Mailbox struct{ options MailboxOptions }

func NewMailbox(o MailboxOptions) (*Mailbox, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.TaskStore == nil {
		return nil, errors.New("teammate mailbox requires private config path and original task lease producer")
	}
	if o.NewID == nil {
		o.NewID = mailboxID
	}
	return &Mailbox{options: o}, nil
}
func mailboxID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("mailbox-%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (m *Mailbox) Path(name, team string) string {
	if team == "" && m.options.TeamName != nil {
		team = m.options.TeamName()
	}
	if team == "" {
		team = "default"
	}
	return filepath.Join(m.options.ConfigDir, "teams", taskstore.SanitizePathComponent(team), "inboxes", taskstore.SanitizePathComponent(name)+".json")
}
func (m *Mailbox) log(e error) {
	if e != nil && m.options.Log != nil {
		m.options.Log(e)
	}
}
func object(data json.RawMessage) protocol.JSONObject {
	var v protocol.JSONObject
	_ = v.UnmarshalJSON(data)
	return v
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func pretty(data json.RawMessage) []byte {
	data = protocol.StringifyValidatedJavaScriptJSON(data)
	var out bytes.Buffer
	if e := json.Indent(&out, data, "", "  "); e != nil {
		return data
	}
	return out.Bytes()
}
func (m *Mailbox) Read(name, team string) json.RawMessage {
	data, e := os.ReadFile(m.Path(name, team))
	if e == nil {
		data, e = protocol.ParseAndStringifyJavaScriptJSON(data)
		if e == nil && string(data) != "null" {
			return data
		}
		if e == nil {
			e = errors.New("Cannot read properties of null (reading 'length')")
		}
	}
	if !errors.Is(e, os.ErrNotExist) {
		m.log(e)
	}
	return []byte(`[]`)
}
func (m *Mailbox) Unread(name, team string) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if e := json.Unmarshal(m.Read(name, team), &values); e != nil {
		return nil, errors.New("messages.filter is not a function. (In 'messages.filter((m) => !m.read)', 'messages.filter' is undefined)")
	}
	out := []json.RawMessage{}
	for _, v := range values {
		if string(v) == "null" {
			return nil, errors.New("Cannot read properties of null (reading 'read')")
		}
		if !truthy(object(v).Get("read")) {
			out = append(out, v)
		}
	}
	return out, nil
}
func truthy(data json.RawMessage) bool {
	if len(data) == 0 || string(data) == "null" || string(data) == "false" || string(data) == `""` {
		return false
	}
	var f float64
	if json.Unmarshal(data, &f) == nil {
		return f != 0
	}
	return true
}
func (m *Mailbox) Write(name, team string, message protocol.JSONObject) (failure error) {
	path := m.Path(name, team)
	if e := os.MkdirAll(filepath.Dir(path), 0777); e != nil {
		return e
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if e == nil {
		_, e = file.Write([]byte(`[]`))
		e = errors.Join(e, file.Close())
	}
	if e != nil && !errors.Is(e, os.ErrExist) {
		m.log(e)
		return nil
	}
	release, e := m.options.TaskStore.AcquireMailboxLease(path)
	if e != nil {
		m.log(e)
		return nil
	}
	defer func() { failure = release() }()
	var messages []json.RawMessage
	if e = json.Unmarshal(m.Read(name, team), &messages); e != nil {
		m.log(errors.New("messages.push is not a function"))
		return nil
	}
	next := message.Clone()
	if id := next.Get("id"); len(id) == 0 || string(id) == "null" {
		id, e := m.options.NewID()
		if e != nil {
			m.log(e)
			return nil
		}
		next.Set("id", raw(id))
	}
	next.Set("read", []byte(`false`))
	messages = append(messages, raw(next))
	m.log(os.WriteFile(path, pretty(raw(messages)), 0666))
	return nil
}
func (m *Mailbox) MarkMatching(name, team string, match func(protocol.JSONObject) bool) (failure error) {
	return m.mark(name, team, match, true, true, false)
}
func (m *Mailbox) mark(name, team string, match func(protocol.JSONObject) bool, alwaysWrite, swallowRelease, visitRead bool) (failure error) {
	path := m.Path(name, team)
	release, e := m.options.TaskStore.AcquireMailboxLease(path)
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			m.log(e)
		}
		return nil
	}
	defer func() {
		if e := release(); !swallowRelease {
			failure = e
		}
	}()
	var messages []json.RawMessage
	if e = json.Unmarshal(m.Read(name, team), &messages); e != nil {
		m.log(e)
		return nil
	}
	changed := false
	for i, value := range messages {
		v := object(value)
		if !v.IsObject() {
			m.log(errors.New("Invalid teammate mailbox entry"))
			return nil
		}
		unread := !truthy(v.Get("read"))
		matches := false
		if unread || visitRead {
			matches = match(v)
		}
		if matches && unread {
			v.Set("read", []byte(`true`))
			messages[i] = raw(v)
			changed = true
		}
	}
	if changed || alwaysWrite && len(messages) > 0 {
		m.log(os.WriteFile(path, pretty(raw(messages)), 0666))
	}
	return nil
}
func (m *Mailbox) MarkAll(name, team string) error {
	return m.mark(name, team, func(protocol.JSONObject) bool { return true }, true, false, false)
}
func (m *Mailbox) MarkIndex(name, team string, index int) error {
	position := -1
	return m.mark(name, team, func(protocol.JSONObject) bool { position++; return position == index }, false, false, true)
}

// Source uses r+ so a missing inbox stays absent. The pinned Bun filesystem
// implementation truncates its successful write; actual Source parity proves
// that behavior rather than inferring it from Node's flag documentation.
func (m *Mailbox) Clear(name, team string) error {
	file, e := os.OpenFile(m.Path(name, team), os.O_WRONLY, 0666)
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			m.log(e)
		}
		return nil
	}
	_, e = file.Write([]byte(`[]`))
	if e == nil {
		e = file.Truncate(2)
	}
	m.log(errors.Join(e, file.Close()))
	return nil
}
func FormatMessages(messages []protocol.JSONObject) string {
	out := []string{}
	for _, m := range messages {
		color, summary := "", ""
		if truthy(m.Get("color")) {
			color = ` color="` + m.String("color") + `"`
		}
		if truthy(m.Get("summary")) {
			summary = ` summary="` + m.String("summary") + `"`
		}
		out = append(out, `<teammate-message teammate_id="`+m.String("from")+`"`+color+summary+">\n"+m.String("text")+"\n</teammate-message>")
	}
	return strings.Join(out, "\n\n")
}
