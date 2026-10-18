package application

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalConfigChannelActualStoresSubmenuAndRevert(t *testing.T) {
	for _, choice := range []string{"stay", "downgrade", "cancel", "latest", "enable-latest", "enable-stable", "env"} {
		t.Run(choice, func(t *testing.T) {
			o := nativeSessionOptions(t, "http://127.0.0.1:1")
			o.Interactive = true
			o.Version = "2.3.4"
			for _, env := range []map[string]string{o.Environment, o.Native.Environment} {
				delete(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
				delete(env, "DISABLE_AUTOUPDATER")
				if choice == "env" {
					env["DISABLE_AUTOUPDATER"] = "1"
				}
			}
			path := filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			channel := "latest"
			if choice == "latest" {
				channel = "stable"
			}
			initial := map[string]any{"autoUpdatesChannel": channel, "minimumVersion": "1.0.0", "futureUser": 7}
			if err := os.WriteFile(path, raw(initial), 0600); err != nil {
				t.Fatal(err)
			}
			seedTerminalReleaseWarmFixture(t, o)
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			if choice == "enable-latest" || choice == "enable-stable" {
				if err = s.process.SetTerminalConfigSetting(context.Background(), "autoUpdates", []byte("false")); err != nil {
					t.Fatal(err)
				}
			}
			var panel *tui.LocalPanel
			emit := func(e tui.Event) error {
				if e.LocalPanel != nil {
					copy := *e.LocalPanel
					panel = &copy
				}
				return nil
			}
			if _, err = s.InvokeTerminalCommand(context.Background(), "config", "", emit); err != nil {
				t.Fatal(err)
			}
			id := panel.ID
			act := func(action, row string) {
				t.Helper()
				if err := s.ApplyLocalPanel(context.Background(), tui.LocalPanelResult{ID: id, Action: action, Row: row}, emit); err != nil || panel.Notice != "" {
					t.Fatal(err, panel.Notice)
				}
			}
			act("edit-config", "autoUpdatesChannel")
			if choice == "cancel" || choice == "env" {
				if !panel.Submenu || panel.DismissAction != "config-back" {
					t.Fatal(panel)
				}
				act("config-back", "")
			} else if choice != "latest" {
				act("config-channel-select", choice)
			}
			data, _ := os.ReadFile(path)
			actual := obj(data)
			if actual.Get("futureUser") == nil {
				t.Fatal(string(data))
			}
			if choice == "stay" && actual.String("minimumVersion") != "2.3.4" || choice == "downgrade" && actual.String("minimumVersion") != "1.0.0" || choice == "latest" && actual.Get("minimumVersion") != nil || choice == "enable-stable" && actual.Get("minimumVersion") != nil {
				t.Fatal(choice, string(data))
			}
			snapshot, err := s.process.TerminalConfigState(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (choice == "enable-latest" || choice == "enable-stable") && string(obj(snapshot.Get("global")).Get("autoUpdates")) != "true" {
				t.Fatal(string(raw(snapshot)))
			}
			act("close", "")
			data, _ = os.ReadFile(path)
			var got, want any
			_ = json.Unmarshal(data, &got)
			_ = json.Unmarshal(raw(initial), &want)
			if string(raw(got)) != string(raw(want)) {
				t.Fatal("Esc did not restore original channel/minimum", string(data), string(raw(initial)))
			}
		})
	}
}
