package teamstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
)

type ConfigOptions struct {
	ConfigDir, Platform string
	TaskStore           *taskstore.Service
	PID                 int
	UUID                func() (string, error)
	Rename              func(string, string) error
	Log                 func(error)
}
type Config struct{ options ConfigOptions }
type TeamFileNotFoundError struct{ Name string }

func (e *TeamFileNotFoundError) Error() string { return fmt.Sprintf("Team %q does not exist", e.Name) }

func NewConfig(o ConfigOptions) (*Config, error) {
	if !filepath.IsAbs(o.ConfigDir) || o.TaskStore == nil || o.PID < 1 {
		return nil, errors.New("team config requires private config path, shared lease, PID and UUID")
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
	}
	if o.UUID == nil {
		o.UUID = func() (string, error) { id, e := mailboxID(); return strings.TrimPrefix(id, "mailbox-"), e }
	}
	if o.Rename == nil {
		o.Rename = os.Rename
	}
	return &Config{options: o}, nil
}
func SanitizeName(name string) string {
	var out strings.Builder
	for _, u := range utf16.Encode([]rune(name)) {
		if u >= 'A' && u <= 'Z' {
			out.WriteByte(byte(u + 32))
		} else if u >= 'a' && u <= 'z' || u >= '0' && u <= '9' {
			out.WriteByte(byte(u))
		} else {
			out.WriteByte('-')
		}
	}
	return out.String()
}
func (c *Config) Directory(name string) string {
	return filepath.Join(c.options.ConfigDir, "teams", SanitizeName(name))
}
func (c *Config) Path(name string) string { return filepath.Join(c.Directory(name), "config.json") }
func validConfig(v protocol.JSONObject) bool {
	data := v.Get("members")
	return v.IsObject() && len(data) > 0 && data[0] == '['
}
func (c *Config) Read(name string) *protocol.JSONObject {
	v := c.ReadAsync(name)
	if v == nil || !validConfig(*v) {
		return nil
	}
	return v
}
func (c *Config) ReadAsync(name string) *protocol.JSONObject {
	raw := c.ReadAsyncRaw(name)
	if raw == nil {
		return nil
	}
	v := object(*raw)
	return &v
}
func (c *Config) ReadAsyncRaw(name string) *json.RawMessage {
	data, e := os.ReadFile(c.Path(name))
	if e != nil {
		if !errors.Is(e, os.ErrNotExist) && c.options.Log != nil {
			c.options.Log(e)
		}
		return nil
	}
	data, e = protocol.ParseAndStringifyJavaScriptJSON(data)
	if e != nil {
		if c.options.Log != nil {
			c.options.Log(e)
		}
		return nil
	}
	value := json.RawMessage(data)
	return &value
}
func (c *Config) replace(name string, config protocol.JSONObject) (err error) {
	id, e := c.options.UUID()
	if e != nil {
		return e
	}
	target := c.Path(name)
	temp := fmt.Sprintf("%s.%d.%s.tmp", target, c.options.PID, id)
	defer os.Remove(temp)
	file, e := os.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = file.Write(pretty(raw(config)))
	e = errors.Join(e, file.Close())
	if e != nil {
		return e
	}
	for attempt := 0; ; attempt++ {
		e = c.options.Rename(temp, target)
		if e == nil {
			return nil
		}
		if c.options.Platform != "win32" && c.options.Platform != "windows" || attempt >= 5 || (!errors.Is(e, os.ErrPermission) && !errors.Is(e, syscall.EBUSY)) {
			return e
		}
		time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
	}
}
func (c *Config) Write(name string, config protocol.JSONObject) (err error) {
	if e := os.MkdirAll(c.Directory(name), 0777); e != nil {
		return e
	}
	release, e := c.options.TaskStore.AcquireTeamConfigLease(c.Path(name))
	if e != nil {
		return e
	}
	defer func() {
		if e := release(); e != nil {
			err = e
		}
	}()
	return c.replace(name, config)
}
func (c *Config) Mutate(name string, fn func(protocol.JSONObject) (protocol.JSONObject, error)) (next protocol.JSONObject, err error) {
	if _, e := os.Stat(c.Directory(name)); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return next, &TeamFileNotFoundError{Name: name}
		}
		return next, e
	}
	release, e := c.options.TaskStore.AcquireTeamConfigLease(c.Path(name))
	if e != nil {
		return next, e
	}
	defer func() {
		if e := release(); e != nil {
			err = e
		}
	}()
	current := c.ReadAsync(name)
	if current == nil || !validConfig(*current) {
		return next, &TeamFileNotFoundError{Name: name}
	}
	previous := string(pretty(raw(*current)))
	next, e = fn(current.Clone())
	if e != nil {
		return next, e
	}
	if !next.IsObject() {
		next = current.Clone()
	}
	if string(pretty(raw(next))) != previous {
		e = c.replace(name, next)
	}
	return next, e
}
