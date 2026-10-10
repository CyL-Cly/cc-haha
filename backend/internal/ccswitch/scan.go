package ccswitch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	Home, Platform string
	Environment    map[string]string
	Existing       []protocol.JSONObject
	Presets        json.RawMessage
}
type Scan struct {
	Available                 bool
	Reason, Source, ConfigDir string
	Entries                   []Entry
}

func (s Scan) Public() protocol.JSONObject {
	candidates := []protocol.JSONObject{}
	for _, entry := range s.Entries {
		candidates = append(candidates, entry.Candidate)
	}
	o := record(field("available", s.Available))
	if s.Reason != "" {
		o.Set("reason", raw(s.Reason))
	}
	if s.Source != "" {
		o.Set("source", raw(s.Source))
	}
	if s.ConfigDir != "" {
		o.Set("configDir", raw(s.ConfigDir))
	}
	o.Set("candidates", raw(candidates))
	return o
}
func (s Scan) Resolve(ids []string) ([]protocol.JSONObject, []protocol.JSONObject) {
	values := []json.RawMessage{}
	for _, id := range ids {
		values = append(values, str(id))
	}
	return s.ResolveJSON(values)
}
func (s Scan) ResolveJSON(ids []json.RawMessage) ([]protocol.JSONObject, []protocol.JSONObject) {
	byID := map[string]Entry{}
	for _, entry := range s.Entries {
		byID[string(protocol.QuoteJavaScriptUTF16(units(entry.Candidate.Get("sourceId"))))] = entry
	}
	inputs, skipped := []protocol.JSONObject{}, []protocol.JSONObject{}
	seen := map[string]bool{}
	for _, id := range ids {
		identity := string(protocol.QuoteJavaScriptUTF16(units(id)))
		if seen[identity] {
			continue
		}
		seen[identity] = true
		entry, ok := byID[identity]
		reason := ""
		if !ok {
			reason = "not-found"
			if !s.Available && s.Reason != "" {
				reason = s.Reason
			}
		} else if entry.CreateInput == nil {
			reason = entry.Candidate.String("skipReason")
			if reason == "" {
				reason = "unsupported-format"
			}
		}
		if reason != "" {
			skipped = append(skipped, record(protocol.JSONField{Name: "sourceId", Value: id}, field("reason", reason)))
		} else {
			inputs = append(inputs, entry.CreateInput.Clone())
		}
	}
	return inputs, skipped
}
func isPath(path string, dir bool) bool {
	info, err := os.Stat(path)
	return err == nil && ((dir && info.IsDir()) || (!dir && info.Mode().IsRegular()))
}
func readJSON(path string) (json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = protocol.DecodeJavaScriptUTF8(b)
	if !json.Valid(b) {
		return nil, errors.New("Invalid JSON")
	}
	return b, nil
}
func expandHome(value, home string) string {
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		return filepath.Join(home, value[2:])
	}
	return value
}
func configDirectory(o Options) string {
	path := ""
	switch o.Platform {
	case "darwin":
		path = filepath.Join(o.Home, "Library", "Application Support", "com.ccswitch.desktop", "app_paths.json")
	case "win32", "windows":
		base := o.Environment["APPDATA"]
		if base == "" {
			base = filepath.Join(o.Home, "AppData", "Roaming")
		}
		path = filepath.Join(base, "com.ccswitch.desktop", "app_paths.json")
	default:
		base := o.Environment["XDG_CONFIG_HOME"]
		if base == "" {
			base = filepath.Join(o.Home, ".config")
		}
		path = filepath.Join(base, "com.ccswitch.desktop", "app_paths.json")
	}
	if b, err := readJSON(path); err == nil {
		if value := trimmed(obj(b).Get("app_config_dir_override")); value != nil {
			dir := expandHome(text(value), o.Home)
			if isPath(dir, true) {
				return dir
			}
		}
	}
	path = filepath.Join(o.Home, ".cc-switch")
	if isPath(path, true) {
		return path
	}
	return ""
}
func sorted(rows []Row) []Row {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		x, y := float64(999999), float64(999999)
		if a.SortIndex != nil {
			x = *a.SortIndex
		}
		if b.SortIndex != nil {
			y = *b.SortIndex
		}
		if x != y {
			return x < y
		}
		x, y = 0, 0
		if a.CreatedAt != nil {
			x = *a.CreatedAt
		}
		if b.CreatedAt != nil {
			y = *b.CreatedAt
		}
		if x != y {
			return x < y
		}
		return less(a.ID, b.ID)
	})
	return rows
}
func legacyRows(data json.RawMessage) []Row {
	root := obj(data)
	rows := []Row{}
	for _, appType := range []string{"claude", "claude-desktop"} {
		app := obj(root.Get(appType))
		providers := obj(app.Get("providers"))
		current := trimmed(app.Get("current"))
		for _, item := range providers.OwnProperties() {
			value := obj(item.Value)
			if !value.IsObject() {
				continue
			}
			id := trimmed(value.Get("id"))
			if id == nil {
				id = item.NameJSON
			}
			name := value.Get("name")
			if !isString(name) {
				name = str("")
			}
			rows = append(rows, Row{ID: id, Name: name, AppType: appType, SettingsConfig: value.Get("settingsConfig"), Meta: value.Get("meta"), Notes: trimmed(value.Get("notes")), SortIndex: number(value.Get("sortIndex"), true), CreatedAt: number(value.Get("createdAt"), true), IsCurrent: current != nil && sameString(current, id)})
		}
	}
	return sorted(rows)
}
func legacyStore(dir string) ([]Row, string) {
	reason := "not-found"
	for _, name := range []string{"config.json", "config.json.migrated"} {
		path := filepath.Join(dir, name)
		if !isPath(path, false) {
			continue
		}
		data, e := readJSON(path)
		if e != nil {
			reason = "unreadable"
			continue
		}
		root := obj(data)
		if root.IsObject() && obj(root.Get("providers")).IsObject() && isString(root.Get("current")) && !obj(root.Get("claude")).IsObject() && !obj(root.Get("claude-desktop")).IsObject() {
			return nil, "version-too-old"
		}
		return legacyRows(data), ""
	}
	return nil, reason
}
func Read(ctx context.Context, o Options) (Scan, error) {
	if !filepath.IsAbs(o.Home) || o.Platform == "" {
		return Scan{}, errors.New("cc-switch scan requires an explicit absolute home and platform")
	}
	scan := Scan{Entries: []Entry{}, ConfigDir: configDirectory(o)}
	if scan.ConfigDir == "" {
		scan.Reason = "not-found"
		return scan, nil
	}
	rows, reason := sqliteRows(ctx, filepath.Join(scan.ConfigDir, "cc-switch.db"))
	scan.Source = "sqlite"
	if reason == "not-found" {
		rows, reason = legacyStore(scan.ConfigDir)
		scan.Source = "json"
	}
	if reason != "" {
		scan.Reason = reason
		scan.Source = ""
		return scan, nil
	}
	var current json.RawMessage
	for _, path := range []string{filepath.Join(scan.ConfigDir, "settings.json"), filepath.Join(o.Home, ".cc-switch", "settings.json")} {
		if b, e := readJSON(path); e == nil {
			current = trimmed(obj(b).Get("currentProviderClaude"))
			if current != nil {
				break
			}
		}
	}
	for _, row := range rows {
		scan.Entries = append(scan.Entries, ToEntry(row, current, o.Existing, o.Presets))
	}
	scan.Entries = Dedupe(scan.Entries)
	scan.Available = true
	return scan, nil
}
