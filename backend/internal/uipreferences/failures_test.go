package uipreferences

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessReadAndWriteFailuresPreserveOriginalAndSourceCleanupOrder(t *testing.T) {
	for _, operation := range []string{"access", "read", "mkdir", "write", "rename"} {
		t.Run(operation, func(t *testing.T) {
			service, boundary, home := newFixture(t)
			ctx := context.Background()
			put(t, home, "desktop-ui.json", `{"future":true}`)
			if _, err := service.ReadPreferences(ctx); err != nil {
				t.Fatal(err)
			}
			boundary.events = nil
			boundary.fault = func(op, path string) error {
				if op == operation && (strings.Contains(path, "desktop-ui.json") || op == "mkdir") {
					return errors.New("fixture denied")
				}
				return nil
			}
			_, err := service.UpdateSidebarPreferences(ctx, json.RawMessage(`{}`))
			if err == nil {
				t.Fatal("failure became success")
			}
			if readFile(t, home, "desktop-ui.json") != `{"future":true}` {
				t.Fatal("failed write changed original")
			}
			if operation == "mkdir" {
				for _, event := range boundary.events {
					if strings.HasPrefix(event, "remove:") {
						t.Fatal("mkdir must stay outside cleanup")
					}
				}
			}
			if operation == "write" || operation == "rename" {
				if !strings.HasPrefix(boundary.events[len(boundary.events)-1], "remove:") {
					t.Fatal("failed temp not cleaned", boundary.events)
				}
			}
			if operation == "access" {
				assertError(t, err, 500, "Failed to access desktop UI preferences: Error: fixture denied")
			}
			if operation == "read" {
				assertError(t, err, 500, "Failed to read cc-haha desktop UI preferences from "+filepath.Join(home, "cc-haha", "desktop-ui.json")+": Error: fixture denied")
			}
			if operation == "write" || operation == "rename" {
				assertError(t, err, 500, "Failed to write desktop-ui.json: Error: fixture denied")
			}
		})
	}
}
func TestAvatarFailuresAndMissingReadsDoNotInventRollback(t *testing.T) {
	service, boundary, home := newFixture(t)
	ctx := context.Background()
	if _, err := service.UpdateProfileAvatar(ctx, []byte("old"), "image/png"); err != nil {
		t.Fatal(err)
	}
	boundary.fault = func(op, path string) error {
		if op == "rename" && strings.Contains(path, "avatar.jpg.tmp") {
			return errors.New("avatar denied")
		}
		return nil
	}
	_, err := service.UpdateProfileAvatar(ctx, []byte("new"), "image/jpeg")
	assertError(t, err, 500, "Failed to write profile avatar: Error: avatar denied")
	if readFile(t, home, "profile/avatar.png") != "old" {
		t.Fatal("failed upload changed original")
	}
	boundary.fault = func(op, path string) error {
		if op == "write" && strings.Contains(path, "desktop-ui.json.tmp") {
			return errors.New("preferences denied")
		}
		return nil
	}
	_, err = service.UpdateProfileAvatar(ctx, []byte("new"), "image/jpeg")
	assertError(t, err, 500, "Failed to write desktop-ui.json: Error: preferences denied")
	if readFile(t, home, "profile/avatar.jpg") != "new" {
		t.Fatal("invented avatar rollback")
	}
	boundary.fault = nil
	if avatar, err := service.ReadProfileAvatar(ctx); err != nil || avatar != nil {
		t.Fatal("missing old referenced avatar should return null", avatar, err)
	}
	put(t, home, "desktop-ui.json", `{"profile":{"avatarFile":"profile/avatar.jpg"}}`)
	boundary.fault = func(op, path string) error {
		if op == "read" && strings.HasSuffix(path, "avatar.jpg") {
			return errors.New("read denied")
		}
		return nil
	}
	_, err = service.ReadProfileAvatar(ctx)
	assertError(t, err, 500, "Failed to read profile avatar: Error: read denied")
	boundary.fault = func(op, path string) error {
		if op == "remove" && strings.Contains(path, "avatar.") {
			return errors.New("remove denied")
		}
		return nil
	}
	if _, err := service.ClearProfileAvatar(ctx); err != nil {
		t.Fatal("best-effort avatar remove became fatal", err)
	}
	if readFile(t, home, "profile/avatar.jpg") != "new" {
		t.Fatal("failed deletion changed bytes")
	}
}
func TestBootstrapAndEntropyFailuresNeverUseAmbientConfig(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("placeholder bootstrap accepted")
	}
	service, _, _ := newFixture(t)
	service.options.ConfigDir = func() string { return "" }
	if _, err := service.ReadPreferences(context.Background()); err == nil {
		t.Fatal("ambient path default accepted")
	}
	if _, err := service.UpdateSidebarPreferences(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty write path accepted")
	}
	if err := service.writePreferences(defaults()); err == nil {
		t.Fatal("empty write selector accepted")
	}
	service, _, home := newFixture(t)
	service.options.Random = bytes.NewReader(nil)
	_, err := service.UpdatePetPreferences(context.Background(), json.RawMessage(`{}`))
	if err == nil || err.Error() != "EOF" {
		t.Fatal(err)
	}
	if strings.Contains(readFileIfPresent(home, "desktop-ui.json"), "schemaVersion") {
		t.Fatal("entropy failure committed preferences")
	}
}
func readFileIfPresent(home, path string) string {
	raw, _ := os.ReadFile(filepath.Join(home, "cc-haha", path))
	return string(raw)
}
