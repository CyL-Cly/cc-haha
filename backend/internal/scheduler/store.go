package scheduler

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

// Source has a module-wide FIFO per physical path, shared by the API and
// dispatcher even when they use different CronService instances.
var mutationLocks sync.Map

func processID() int { return os.Getpid() }
func pathLock(path string) *sync.Mutex {
	lock, _ := mutationLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	return lock.(*sync.Mutex)
}

type taskStore struct{ options Options }

func (s *taskStore) path() string { return filepath.Join(s.options.ConfigDir, "scheduled_tasks.json") }
func (s *taskStore) runsPath() string {
	return filepath.Join(s.options.ConfigDir, "scheduled_tasks_log.json")
}
func jsTrim(value string) string {
	return strings.TrimFunc(value, func(r rune) bool {
		return r == '\t' || r == '\n' || r == '\v' || r == '\f' || r == '\r' || r == ' ' || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
}
func blank(data string) bool {
	for _, r := range data {
		if r != 0 && jsTrim(string(r)) != "" {
			return false
		}
	}
	return true
}
func jsTruthy(value json.RawMessage) bool {
	value = bytes.TrimSpace(value)
	if len(value) == 0 || string(value) == "null" || string(value) == "false" || string(value) == "0" || string(value) == "-0" {
		return false
	}
	if value[0] == '"' {
		units, e := protocol.DecodeJavaScriptJSONString(value)
		return e == nil && len(units) > 0
	}
	if value[0] != '{' && value[0] != '[' && string(value) != "true" {
		var number float64
		return json.Unmarshal(value, &number) == nil && number != 0 && !math.IsNaN(number)
	}
	return true
}
func (s *taskStore) read(path, key string) (protocol.JSONObject, []protocol.JSONObject, []byte, error) {
	data, e := s.options.Files.ReadFile(path)
	if errors.Is(e, fs.ErrNotExist) {
		o := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`[]`)})
		return o, []protocol.JSONObject{}, nil, nil
	}
	if e != nil {
		return protocol.JSONObject{}, nil, nil, e
	}
	text := string(protocol.DecodeJavaScriptUTF8(data))
	if blank(text) {
		o := protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`[]`)})
		return o, []protocol.JSONObject{}, data, nil
	}
	text = strings.TrimPrefix(text, "\ufeff")
	if !json.Valid([]byte(text)) {
		message, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(text)
		if invalid {
			units, _ := protocol.DecodeJavaScriptJSONString(message)
			return protocol.JSONObject{}, nil, data, errors.New(string(utf16.Decode(units)))
		}
		return protocol.JSONObject{}, nil, data, errors.New("Invalid JSON")
	}
	var file protocol.JSONObject
	if e = file.UnmarshalJSON([]byte(text)); e != nil || !file.IsObject() {
		if strings.TrimSpace(text) == "null" {
			return file, nil, data, errors.New("null is not an object (evaluating 'parsed." + key + "')")
		}
		file = protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`[]`)})
		return file, []protocol.JSONObject{}, data, nil
	}
	var rows []protocol.JSONObject
	if len(file.Get(key)) == 0 || file.Get(key)[0] != '[' {
		file = protocol.NewJSONObject(protocol.JSONField{Name: key, Value: []byte(`[]`)})
		rows = []protocol.JSONObject{}
	} else if e = json.Unmarshal(file.Get(key), &rows); e != nil {
		return file, nil, data, &CapabilityError{"non-object historical " + key + " entries"}
	}
	if rows == nil {
		rows = []protocol.JSONObject{}
	}
	return file, rows, data, nil
}
func (s *taskStore) readTasks() (protocol.JSONObject, []protocol.JSONObject, error) {
	o, rows, _, e := s.read(s.path(), "tasks")
	if e != nil {
		return o, nil, internal("Failed to read scheduled tasks: " + e.Error())
	}
	return o, rows, nil
}
func (s *taskStore) random(n int) (string, error) {
	b, e := s.options.RandomBytes(n)
	if e != nil {
		return "", e
	}
	if len(b) != n {
		return "", errors.New("scheduled task random producer returned wrong byte length")
	}
	return hex.EncodeToString(b), nil
}
func pretty(o protocol.JSONObject) ([]byte, error) {
	b, e := o.MarshalJSON()
	if e != nil {
		return nil, e
	}
	var out bytes.Buffer
	if e = json.Indent(&out, protocol.StringifyValidatedJavaScriptJSON(b), "", "  "); e != nil {
		return nil, e
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}
func (s *taskStore) writeTasks(o protocol.JSONObject) error {
	path := s.path()
	data, e := pretty(o)
	if e != nil {
		return e
	}
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		random, e := s.random(6)
		if e != nil {
			return e
		}
		temp := fmt.Sprintf("%s.tmp.%d.%d.%s", path, processID(), s.options.Now().UnixMilli(), random)
		last = s.options.Files.MkdirAll(filepath.Dir(path), 0777)
		if last == nil {
			last = s.options.Files.WriteFile(temp, data, 0666)
		}
		if last == nil {
			last = s.options.Files.Rename(temp, path)
		}
		if last == nil {
			return nil
		}
		_ = s.options.Files.Remove(temp)
		if !errors.Is(last, fs.ErrNotExist) && !errors.Is(last, syscall.EPERM) {
			break
		}
	}
	return internal("Failed to write scheduled tasks: " + last.Error())
}
func (s *taskStore) list() ([]protocol.JSONObject, error) {
	_, rows, e := s.readTasks()
	if e != nil {
		return nil, e
	}
	out := []protocol.JSONObject{}
	for _, row := range rows {
		row = row.Clone()
		row.Set("permissionMode", quoted("bypassPermissions"))
		out = append(out, row)
	}
	return out, nil
}
func (s *taskStore) create(input protocol.JSONObject) (protocol.JSONObject, error) {
	if !jsTruthy(input.Get("cron")) || !jsTruthy(input.Get("prompt")) {
		return protocol.JSONObject{}, bad(`Fields "cron" and "prompt" are required`)
	}
	lock := pathLock(s.path())
	lock.Lock()
	defer lock.Unlock()
	file, rows, e := s.readTasks()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	id, e := s.random(4)
	if e != nil {
		return protocol.JSONObject{}, e
	}
	task := input.Clone()
	task.Set("permissionMode", quoted("bypassPermissions"))
	task.Set("id", quoted(id))
	task.Set("createdAt", raw(s.options.Now().UnixMilli()))
	rows = append(rows, task)
	file.Set("tasks", raw(rows))
	if e = s.writeTasks(file); e != nil {
		return protocol.JSONObject{}, e
	}
	return task, nil
}
func (s *taskStore) update(id string, patch protocol.JSONObject) (protocol.JSONObject, error) {
	lock := pathLock(s.path())
	lock.Lock()
	defer lock.Unlock()
	file, rows, e := s.readTasks()
	if e != nil {
		return protocol.JSONObject{}, e
	}
	for i, task := range rows {
		if task.String("id") != id {
			continue
		}
		next := task.Clone()
		for _, field := range patch.OwnProperties() {
			if bytes.Equal(field.NameJSON, quoted("id")) || bytes.Equal(field.NameJSON, quoted("createdAt")) {
				continue
			}
			_ = next.SetPropertyJSON(field.NameJSON, field.Value)
		}
		next.Set("permissionMode", quoted("bypassPermissions"))
		rows[i] = next
		file.Set("tasks", raw(rows))
		if e = s.writeTasks(file); e != nil {
			return protocol.JSONObject{}, e
		}
		return next, nil
	}
	return protocol.JSONObject{}, missing("Task not found: " + id)
}
func (s *taskStore) delete(id string) error {
	lock := pathLock(s.path())
	lock.Lock()
	defer lock.Unlock()
	file, rows, e := s.readTasks()
	if e != nil {
		return e
	}
	for i, task := range rows {
		if task.String("id") == id {
			rows = append(rows[:i], rows[i+1:]...)
			file.Set("tasks", raw(rows))
			return s.writeTasks(file)
		}
	}
	return missing("Task not found: " + id)
}
func (s *taskStore) lastFired(id, stamp string) error {
	lock := pathLock(s.path())
	lock.Lock()
	defer lock.Unlock()
	file, rows, e := s.readTasks()
	if e != nil {
		return e
	}
	for i, task := range rows {
		if task.String("id") == id {
			task.Set("lastFiredAt", quoted(stamp))
			rows[i] = task
			file.Set("tasks", raw(rows))
			return s.writeTasks(file)
		}
	}
	return nil
}
func (s *taskStore) mutateRuns(run protocol.JSONObject, replace bool) error {
	path := s.runsPath()
	lock := pathLock(path)
	lock.Lock()
	defer lock.Unlock()
	file, rows, _, e := s.read(path, "runs")
	if e != nil {
		return e
	}
	found := false
	if replace {
		for i, row := range rows {
			if row.String("id") == run.String("id") {
				rows[i] = run.Clone()
				found = true
				break
			}
		}
	}
	if !found {
		rows = append(rows, run.Clone())
	}
	counts := map[string]int{}
	keep := make([]bool, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		id := rows[i].String("taskId")
		if counts[id] < 100 {
			keep[i] = true
			counts[id]++
		}
	}
	trimmed := []protocol.JSONObject{}
	for i, row := range rows {
		if keep[i] {
			trimmed = append(trimmed, row)
		}
	}
	file.Set("runs", raw(trimmed))
	data, e := pretty(file)
	if e != nil {
		return e
	}
	random, e := s.random(6)
	if e != nil {
		return e
	}
	temp := fmt.Sprintf("%s.tmp.%d.%s", path, processID(), random)
	if e = s.options.Files.MkdirAll(filepath.Dir(path), 0777); e != nil {
		return e
	}
	if e = s.options.Files.WriteFile(temp, data, 0666); e == nil {
		e = s.options.Files.Rename(temp, path)
	}
	if e != nil {
		_ = s.options.Files.Remove(temp)
	} else if s.options.afterRunWrite != nil {
		s.options.afterRunWrite(path, data, trimmed)
	}
	return e
}
func fileToken(data []byte) string {
	if data == nil {
		return "file:missing"
	}
	sum := sha256.Sum256(protocol.DecodeJavaScriptUTF8(data))
	return "file:" + base64.RawURLEncoding.EncodeToString(sum[:])
}
