// Package teamstore owns the original private team files and teammate inboxes.
// Task transactions and cross-process leases reuse the existing taskstore.
package teamstore

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

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
func parseInbox(data []byte) ([]json.RawMessage, bool) {
	if len(bytes.TrimSpace(data)) == 0 {
		return []json.RawMessage{}, true
	}
	var messages []json.RawMessage
	if json.Unmarshal(protocol.DecodeJavaScriptUTF8(data), &messages) != nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, false
	}
	kept := []json.RawMessage{}
	for _, v := range messages {
		if object(v).IsObject() {
			kept = append(kept, v)
		}
	}
	return kept, true
}
func (m *Mailbox) Read(name, team string) json.RawMessage {
	data, e := os.ReadFile(m.Path(name, team))
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			m.log(e)
		}
		return []byte("[]")
	}
	messages, ok := parseInbox(data)
	if !ok {
		m.log(errors.New("damaged teammate inbox"))
		return []byte("[]")
	}
	return raw(messages)
}
func (m *Mailbox) Unread(name, team string) ([]json.RawMessage, error) {
	var messages []json.RawMessage
	_ = json.Unmarshal(m.Read(name, team), &messages)
	out := []json.RawMessage{}
	for _, v := range messages {
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
func MailboxIdentity(v protocol.JSONObject) string {
	parts := []json.RawMessage{raw("id:"), v.Get("id")}
	if !truthy(v.Get("id")) {
		parts = []json.RawMessage{raw("legacy:"), v.Get("from"), raw("|"), v.Get("timestamp"), raw("|"), v.Get("text")}
	}
	identity, e := protocol.JoinJavaScriptValuesJSON(parts, "")
	if e != nil {
		return ""
	}
	return string(identity)
}
func MailboxHistoryPath(path string) string {
	return strings.TrimSuffix(path, ".json") + ".history.jsonl"
}
func (m *Mailbox) update(path string, modify func([]json.RawMessage) []json.RawMessage) error {
	release, e := m.options.TaskStore.AcquireMailboxLease(path)
	if e != nil {
		return e
	}
	defer func() { m.log(release()) }()
	data, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	messages, valid := parseInbox(data)
	if !valid {
		backup := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixMilli())
		if e = os.WriteFile(backup, data, 0600); e != nil {
			return e
		}
		m.log(fmt.Errorf("damaged inbox preserved at %s", backup))
		messages = []json.RawMessage{}
	}
	next := modify(messages)
	unread, read := []json.RawMessage{}, []json.RawMessage{}
	for _, v := range next {
		if truthy(object(v).Get("read")) {
			read = append(read, v)
		} else {
			unread = append(unread, v)
		}
	}
	if e = appendInboxHistory(MailboxHistoryPath(path), read); e != nil {
		return e
	}
	content := protocol.StringifyValidatedJavaScriptJSON(raw(unread))
	if valid && bytes.Equal(data, content) {
		return nil
	}
	return replaceInbox(path, content)
}
func appendInboxHistory(path string, messages []json.RawMessage) (failure error) {
	if len(messages) == 0 {
		return nil
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer func() { failure = errors.Join(failure, f.Close()) }()
	stat, e := f.Stat()
	if e != nil {
		return e
	}
	payload := []byte{}
	if stat.Size() > 0 {
		last := []byte{0}
		if _, e = f.ReadAt(last, stat.Size()-1); e != nil {
			return e
		}
		if last[0] != 10 {
			payload = append(payload, 10)
		}
	}
	for _, v := range messages {
		next := object(v).Clone()
		next.Set("read", raw(true))
		payload = append(payload, protocol.StringifyValidatedJavaScriptJSON(raw(next))...)
		payload = append(payload, 10)
	}
	n, e := f.Write(payload)
	if e == nil && n != len(payload) {
		e = io.ErrShortWrite
	}
	return e
}
func replaceInbox(path string, content []byte) error {
	id, e := mailboxID()
	if e != nil {
		return e
	}
	tmp := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), id)
	defer os.Remove(tmp)
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	n, e := f.Write(content)
	if e == nil && n != len(content) {
		e = io.ErrShortWrite
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	for attempt := 0; ; attempt++ {
		e = os.Rename(tmp, path)
		if e == nil {
			return nil
		}
		if runtime.GOOS != "windows" || attempt >= 5 || !errors.Is(e, os.ErrPermission) && !strings.Contains(strings.ToLower(e.Error()), "being used") {
			return e
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
}
func (m *Mailbox) Write(name, team string, message protocol.JSONObject) error {
	path := m.Path(name, team)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e == nil {
		_, e = f.Write([]byte("[]"))
		e = errors.Join(e, f.Close())
	}
	if e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	next := message.Clone()
	if id := next.Get("id"); len(id) == 0 || string(id) == "null" {
		id, e := m.options.NewID()
		if e != nil {
			return e
		}
		next.Set("id", raw(id))
	}
	next.Set("read", raw(false))
	return m.update(path, func(messages []json.RawMessage) []json.RawMessage { return append(messages, raw(next)) })
}
func (m *Mailbox) Claim(name, team string, candidates []json.RawMessage) ([]json.RawMessage, error) {
	if len(candidates) == 0 {
		return []json.RawMessage{}, nil
	}
	identities := map[string]bool{}
	for _, v := range candidates {
		identities[MailboxIdentity(object(v))] = true
	}
	claimed := []json.RawMessage{}
	e := m.update(m.Path(name, team), func(messages []json.RawMessage) []json.RawMessage {
		for i, v := range messages {
			value := object(v)
			if !truthy(value.Get("read")) && identities[MailboxIdentity(value)] {
				claimed = append(claimed, v)
				value = value.Clone()
				value.Set("read", raw(true))
				messages[i] = raw(value)
			}
		}
		return messages
	})
	if errors.Is(e, os.ErrNotExist) {
		return []json.RawMessage{}, nil
	}
	if e != nil {
		return nil, e
	}
	return claimed, nil
}
func (m *Mailbox) MarkMatching(name, team string, match func(protocol.JSONObject) bool) error {
	e := m.update(m.Path(name, team), func(messages []json.RawMessage) []json.RawMessage {
		for i, v := range messages {
			value := object(v)
			if !truthy(value.Get("read")) && match(value) {
				value = value.Clone()
				value.Set("read", raw(true))
				messages[i] = raw(value)
			}
		}
		return messages
	})
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}
func (m *Mailbox) MarkAll(name, team string) error {
	batch, e := m.Unread(name, team)
	if e != nil {
		return e
	}
	_, e = m.Claim(name, team, batch)
	return e
}
func (m *Mailbox) MarkIndex(name, team string, index int) error {
	var messages []json.RawMessage
	_ = json.Unmarshal(m.Read(name, team), &messages)
	if index < 0 || index >= len(messages) {
		return nil
	}
	_, e := m.Claim(name, team, []json.RawMessage{messages[index]})
	return e
}
func (m *Mailbox) Clear(name, team string) error {
	return m.MarkMatching(name, team, func(protocol.JSONObject) bool { return true })
}
func (m *Mailbox) ReadHistory(name, team string) json.RawMessage {
	return m.ReadHistoryAtPath(m.Path(name, team))
}
func (m *Mailbox) ReadHistoryAtPath(path string) json.RawMessage {
	live, e := os.ReadFile(path)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		m.log(e)
	}
	messages, _ := parseInbox(live)
	history := []json.RawMessage{}
	f, e := os.Open(MailboxHistoryPath(path))
	if e == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 65536), 8*1024*1024)
		for scanner.Scan() {
			v := object(scanner.Bytes())
			if v.IsObject() {
				v.Set("read", raw(true))
				history = append(history, raw(v))
			}
		}
		m.log(scanner.Err())
		m.log(f.Close())
	} else if !errors.Is(e, os.ErrNotExist) {
		m.log(e)
	}
	seen := map[string]bool{}
	out := []json.RawMessage{}
	for _, v := range append(history, messages...) {
		identity := MailboxIdentity(object(v))
		if !seen[identity] {
			seen[identity] = true
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return object(out[a]).String("timestamp") < object(out[b]).String("timestamp") })
	return raw(out)
}
func FormatMessages(messages []protocol.JSONObject) string {
	out := []string{}
	for _, v := range messages {
		color, summary := "", ""
		if truthy(v.Get("color")) {
			color = ` color="` + EscapeXMLAttr(v.String("color")) + `"`
		}
		if truthy(v.Get("summary")) {
			summary = ` summary="` + EscapeXMLAttr(v.String("summary")) + `"`
		}
		out = append(out, `<teammate-message teammate_id="`+EscapeXMLAttr(v.String("from"))+`"`+color+summary+">\n"+v.String("text")+"\n</teammate-message>")
	}
	return strings.Join(out, "\n\n")
}

func EscapeXMLAttr(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(value)
}
func UnescapeXMLAttr(value string) string {
	return strings.NewReplacer("&quot;", `"`, "&apos;", "'", "&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(value)
}
func EscapeXMLAttrJSON(value json.RawMessage) (json.RawMessage, error) {
	original, e := protocol.ExactJavaScriptStringJSON(value)
	if e != nil {
		return nil, e
	}
	units, e := protocol.DecodeJavaScriptJSONString(original)
	if e != nil {
		return nil, e
	}
	out := []uint16{}
	for _, u := range units {
		replacement := ""
		switch u {
		case '&':
			replacement = "&amp;"
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '"':
			replacement = "&quot;"
		case '\'':
			replacement = "&apos;"
		}
		if replacement == "" {
			out = append(out, u)
		} else {
			for _, c := range replacement {
				out = append(out, uint16(c))
			}
		}
	}
	return protocol.QuoteJavaScriptUTF16(out), nil
}
