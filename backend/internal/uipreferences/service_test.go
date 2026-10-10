package uipreferences

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type fixtureFS struct {
	storage.FileSystem
	mu     sync.Mutex
	events []string
	fault  func(string, string) error
}

func (boundary *fixtureFS) before(op, path string) error {
	boundary.mu.Lock()
	boundary.events = append(boundary.events, op+":"+path)
	boundary.mu.Unlock()
	if boundary.fault != nil {
		return boundary.fault(op, path)
	}
	return nil
}
func (boundary *fixtureFS) ReadFile(path string) ([]byte, error) {
	if err := boundary.before("read", path); err != nil {
		return nil, err
	}
	return boundary.FileSystem.ReadFile(path)
}
func (boundary *fixtureFS) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := boundary.before("write", path); err != nil {
		return err
	}
	return boundary.FileSystem.WriteFile(path, data, mode)
}
func (boundary *fixtureFS) MkdirAll(path string, mode fs.FileMode) error {
	if err := boundary.before("mkdir", path); err != nil {
		return err
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}
func (boundary *fixtureFS) Rename(old, next string) error {
	if err := boundary.before("rename", old+" -> "+next); err != nil {
		return err
	}
	return boundary.FileSystem.Rename(old, next)
}
func (boundary *fixtureFS) Remove(path string) error {
	if err := boundary.before("remove", path); err != nil {
		return err
	}
	return boundary.FileSystem.Remove(path)
}
func (boundary *fixtureFS) access(path string) error {
	if err := boundary.before("access", path); err != nil {
		return err
	}
	_, err := os.Stat(path)
	return err
}
func newFixture(t *testing.T) (*Service, *fixtureFS, string) {
	t.Helper()
	home := t.TempDir()
	boundary := &fixtureFS{FileSystem: storage.New(storage.Options{}).FileSystem()}
	store := storage.New(storage.Options{FS: boundary, Warn: func(string) {}})
	upgrader, err := migrations.New(migrations.Options{Store: store, Access: boundary.access})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{Store: store, Upgrader: upgrader, ConfigDir: func() string { return home }, Access: boundary.access, Now: func() time.Time { return time.UnixMilli(1700000000000) }, Random: bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 1000)), PID: 42})
	if err != nil {
		t.Fatal(err)
	}
	return service, boundary, home
}
func put(t *testing.T, home, path, raw string) {
	t.Helper()
	path = filepath.Join(home, "cc-haha", path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func readFile(t *testing.T, home, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "cc-haha", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func field(t *testing.T, pref Preferences, name string) json.RawMessage {
	t.Helper()
	value, valid := parseObject(json.RawMessage(pref))
	if !valid {
		t.Fatal("invalid prefs")
	}
	return value.get(name)
}
func assertError(t *testing.T, err error, status int, message string) {
	t.Helper()
	var api *settings.APIError
	if !errors.As(err, &api) || api.StatusCode != status || api.Message != message {
		t.Fatalf("error = %v; want %d %s", err, status, message)
	}
}

func TestDefaultsReadOnlyOldNormalizationAndUnknowns(t *testing.T) {
	service, _, home := newFixture(t)
	read, err := service.ReadPreferences(context.Background())
	if err != nil || read.Exists || string(field(t, read.Preferences, "schemaVersion")) != "6" {
		t.Fatal(read, err)
	}
	if _, err := os.Stat(filepath.Join(home, "cc-haha")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing preferences wrote state")
	}
	old := `{"schemaVersion":3,"future":9007199254740993,"sidebar":{"projectOrder":["a",4,"a","b"],"pinnedProjects":["a"],"future":true},"profile":{"displayName":"  Alice  \n Example ","subtitle":"  ","futureProfile":true},"pet":{"enabled":"bad","selectedPetId":"bad id","size":12,"lastSessionId":" session ","futurePet":true},"voiceInput":{"providerId":"  provider ","language":"invalid","downloadSource":"mirror","futureVoice":true},"projectDisplayNames":{"__proto__":"  Prototype   name  ","  key  ":" Name ","bad":1}}`
	put(t, home, "desktop-ui.json", old)
	read, err = service.ReadPreferences(context.Background())
	if err != nil || !read.Exists {
		t.Fatal(err)
	}
	if readFile(t, home, "desktop-ui.json") != old {
		t.Fatal("normalized read rewrote old bytes")
	}
	if string(field(t, read.Preferences, "future")) != "9007199254740993" {
		t.Fatal("unknown numeric business value rounded prematurely")
	}
	next, err := service.UpdateSidebarPreferences(context.Background(), json.RawMessage(`{"hiddenProjects":["c"],"futurePatch":true}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := next.MarshalJSON()
	if err != nil || !strings.Contains(string(encoded), `"future":9007199254740992`) {
		t.Fatal(string(encoded), err)
	}
	if !strings.Contains(readFile(t, home, "desktop-ui.json"), `"future": 9007199254740992`) {
		t.Fatal("save omitted JS serialize")
	}
	for _, name := range []string{"profile", "pet", "voiceInput", "projectDisplayNames"} {
		if !reflect.DeepEqual(field(t, read.Preferences, name), field(t, next, name)) {
			t.Fatal("sidebar save changed normalized sibling", name)
		}
	}
}
func TestRecoveryExistenceAndRealUpgradeBeforeRead(t *testing.T) {
	for _, raw := range []string{`{bad`, `[]`, `null`, `"wrong"`} {
		service, _, home := newFixture(t)
		put(t, home, "desktop-ui.json", raw)
		result, err := service.ReadPreferences(context.Background())
		if err != nil || result.Exists {
			t.Fatal(result, err)
		}
		entries, _ := os.ReadDir(filepath.Join(home, "cc-haha"))
		if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "desktop-ui.json.invalid-") {
			t.Fatal(entries)
		}
		backup, _ := os.ReadFile(filepath.Join(home, "cc-haha", entries[0].Name()))
		if string(backup) != raw {
			t.Fatal("quarantine changed source")
		}
	}
	service, boundary, home := newFixture(t)
	put(t, home, "desktop-ui.json", " \n\t\ufeff")
	result, err := service.ReadPreferences(context.Background())
	if err != nil || !result.Exists || readFile(t, home, "desktop-ui.json") != " \n\t\ufeff" {
		t.Fatal(result, err)
	}
	put(t, home, "desktop-ui.json", `{bad`)
	boundary.fault = func(op, path string) error {
		if op == "rename" && strings.Contains(path, ".invalid-") {
			return errors.New("denied")
		}
		return nil
	}
	result, err = service.ReadPreferences(context.Background())
	if err != nil || !result.Exists || readFile(t, home, "desktop-ui.json") != `{bad` {
		t.Fatal("best-effort quarantine changed existence", result, err)
	}
	service, _, home = newFixture(t)
	put(t, home, "settings.json", `{"env":null,"future":true}`)
	if _, err := service.ReadPreferences(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, home, "settings.json"), `"env": {}`) {
		t.Fatal("actual upgrader not invoked")
	}
}
func TestProfilePatchValidationAndClientAvatarFieldsCannotOverrideManagedState(t *testing.T) {
	service, _, home := newFixture(t)
	put(t, home, "desktop-ui.json", `{"schemaVersion":999,"profile":{"displayName":"Old","subtitle":"Old subtitle","avatarFile":"profile/avatar.png","avatarUpdatedAt":"old","future":true},"future":true}`)
	for _, test := range []struct{ raw, message string }{{`null`, "Profile preferences must be an object"}, {`{"displayName":null}`, "displayName must be a string"}, {`{"subtitle":0}`, "subtitle must be a string"}, {`{"displayName":"  "}`, "displayName must be between 1 and 80 characters"}, {`{"subtitle":"` + strings.Repeat("s", 161) + `"}`, "subtitle must be between 1 and 160 characters"}} {
		_, err := service.UpdateProfilePreferences(context.Background(), json.RawMessage(test.raw))
		assertError(t, err, 400, test.message)
	}
	next, err := service.UpdateProfilePreferences(context.Background(), json.RawMessage(`{"displayName":"  New   name ","avatarFile":"profile/avatar.jpg","avatarUpdatedAt":"forged","newUnknown":"ignored"}`))
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := parseObject(field(t, next, "profile"))
	if !stringIs(profile.get("displayName"), "New name") || !stringIs(profile.get("avatarUpdatedAt"), "old") || profile.get("newUnknown") != nil || string(field(t, next, "schemaVersion")) != "999" {
		t.Fatal(profile)
	}
}

func TestInheritedMIMELookupRetainsReadValidationOrderAndNeverWritesAvatar(t *testing.T) {
	for _, mime := range []string{"constructor", "__proto__"} {
		t.Run(mime, func(t *testing.T) {
			service, boundary, home := newFixture(t)
			put(t, home, "desktop-ui.json", `{broken`)
			_, err := service.UpdateProfileAvatar(context.Background(), []byte{1}, mime)
			assertError(t, err, 400, "Invalid avatar file path")
			entries, err := os.ReadDir(filepath.Join(home, "cc-haha"))
			if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "desktop-ui.json.invalid-") {
				t.Fatal("read did not quarantine the corrupt source before validation", entries, err)
			}
			for _, event := range boundary.events {
				if strings.HasPrefix(event, "write:") || strings.Contains(event, "profile") {
					t.Fatal("inherited MIME wrote or accessed a profile image", event)
				}
			}
			boundary.events = nil
			_, err = service.UpdateProfileAvatar(context.Background(), make([]byte, 2000001), mime)
			assertError(t, err, 400, "Avatar image is too large")
			if len(boundary.events) != 0 {
				t.Fatal("oversized inherited MIME accessed preferences")
			}
		})
	}
}

func TestProjectNameValidationPrototypeKeysCapAndFIFOAcrossInstances(t *testing.T) {
	service, _, _ := newFixture(t)
	ctx := context.Background()
	for _, test := range []struct{ raw, message string }{{`null`, "Project display name update must be an object"}, {`{}`, "projectKey must be a non-empty string up to 4096 characters"}, {`{"projectKey":" \ufeff "}`, "projectKey must be a non-empty string up to 4096 characters"}, {`{"projectKey":"key","displayName":null}`, ""}, {`{"projectKey":"key"}`, "displayName must be a non-empty string up to 80 characters"}} {
		_, err := service.UpdateProjectDisplayName(ctx, json.RawMessage(test.raw))
		if test.message == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertError(t, err, 400, test.message)
		}
	}
	for _, key := range []string{"__proto__", "constructor", "  exact key  "} {
		raw := object{}
		raw.set("projectKey", quoted(key))
		raw.set("displayName", quoted("  A   name "))
		if _, err := service.UpdateProjectDisplayName(ctx, raw.raw()); err != nil {
			t.Fatal(err)
		}
	}
	second, err := New(service.options)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index, target := range []*Service{service, second} {
		wait.Add(1)
		go func(index int, target *Service) {
			defer wait.Done()
			_, err := target.UpdateProjectDisplayName(ctx, json.RawMessage(fmtName(index)))
			if err != nil {
				t.Error(err)
			}
		}(index, target)
	}
	wait.Wait()
	result, err := service.ReadPreferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names, _ := parseObject(field(t, result.Preferences, "projectDisplayNames"))
	if len(names) != 5 {
		t.Fatal("concurrent names lost", names)
	}
	_, err = service.UpdateProjectDisplayName(ctx, json.RawMessage(`{"projectKey":"__proto__","displayName":null}`))
	if err != nil {
		t.Fatal(err)
	}
	names = object{}
	for index := 0; index < 2000; index++ {
		names.set(strconvName(index), quoted("name"))
	}
	current, _ := parseObject(json.RawMessage(result.Preferences))
	current.set("projectDisplayNames", names.raw())
	if err := service.writePreferences(Preferences(current.raw())); err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateProjectDisplayName(ctx, json.RawMessage(`{"projectKey":"new","displayName":"name"}`))
	assertError(t, err, 400, "Project display names cannot exceed 2000 entries")
	if _, err := service.UpdateProjectDisplayName(ctx, json.RawMessage(`{"projectKey":"0","displayName":"updated"}`)); err != nil {
		t.Fatal(err)
	}
}
func fmtName(index int) string {
	return `{"projectKey":"concurrent-` + strconvName(index) + `","displayName":"name"}`
}
func strconvName(index int) string { return strconv.Itoa(index) }

func TestPetVoicePatchesAndAvatarPersistenceCleanup(t *testing.T) {
	service, _, home := newFixture(t)
	ctx := context.Background()
	pet, err := service.UpdatePetPreferences(ctx, json.RawMessage(`{"enabled":true,"size":250,"selectedPetId":" custom:my-pet ","future":true,"lastSessionId":"session"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := parseObject(field(t, pet, "pet"))
	if string(value.get("size")) != "192" || !stringIs(value.get("selectedPetId"), "custom:my-pet") {
		t.Fatal(value)
	}
	voice, err := service.UpdateVoiceInputPreferences(ctx, json.RawMessage(`{"enabled":true,"language":"yue","downloadSource":"official","providerId":" p ","future":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(field(t, voice, "voiceInput"), []byte(`"providerId":"p"`)) {
		t.Fatal(voice)
	}
	put(t, home, "profile/avatar.jpg", "stale")
	put(t, home, "profile/keep.txt", "protected")
	avatar, err := service.UpdateProfileAvatar(ctx, []byte{0, 1, 2, 255}, " IMAGE/PNG ; charset=utf-8 ")
	if err != nil {
		t.Fatal(err)
	}
	profile, _ := parseObject(field(t, avatar, "profile"))
	if !stringIs(profile.get("avatarUpdatedAt"), "2023-11-14T22:13:20.000Z") {
		t.Fatal(profile)
	}
	read, err := service.ReadProfileAvatar(ctx)
	if err != nil || read.ContentType != "image/png" || !bytes.Equal(read.Bytes, []byte{0, 1, 2, 255}) {
		t.Fatal(read, err)
	}
	if _, err := os.Stat(filepath.Join(home, "cc-haha/profile/avatar.jpg")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("stale avatar kept")
	}
	if _, err := service.ClearProfileAvatar(ctx); err != nil {
		t.Fatal(err)
	}
	if read, err := service.ReadProfileAvatar(ctx); err != nil || read != nil {
		t.Fatal(read, err)
	}
	if readFile(t, home, "profile/keep.txt") != "protected" {
		t.Fatal("clear removed unrelated file")
	}
	for _, mime := range []string{"image/svg+xml", "", "\u0085image/png\u0085", "İMAGE/PNG"} {
		_, err := service.UpdateProfileAvatar(ctx, nil, mime)
		assertError(t, err, 400, "Unsupported avatar type")
	}
	_, err = service.UpdateProfileAvatar(ctx, make([]byte, 2000001), "image/png")
	assertError(t, err, 400, "Avatar image is too large")
}
