// Package storagemetadata transforms only a private staged storage copy.
package storagemetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/connectors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type relocation struct {
	source, target, stage string
	roots                 []string
}

func inside(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
func object(raw json.RawMessage, label string) (protocol.JSONObject, error) {
	var value protocol.JSONObject
	if err := value.UnmarshalJSON(raw); err != nil || !value.IsObject() {
		return value, fmt.Errorf("Invalid %s", label)
	}
	return value, nil
}
func encode(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func (r relocation) assertPath(file string) error {
	if !inside(r.stage, file) {
		return errors.New("Storage metadata path escaped staging directory")
	}
	rel, _ := filepath.Rel(r.stage, file)
	current := r.stage
	segments := []string{}
	if rel != "." {
		segments = strings.Split(rel, string(filepath.Separator))
	}
	for _, part := range segments {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Linked storage metadata cannot be migrated")
		}
		if current == file {
			independent, err := independentFile(current, info)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || !independent {
				return errors.New("Storage metadata must be a regular independent file")
			}
		}
	}
	return nil
}
func (r relocation) read(file string) (*protocol.JSONObject, error) {
	if err := r.assertPath(file); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	value, err := object(data, "storage metadata")
	if err != nil {
		return nil, err
	}
	return &value, nil
}
func (r relocation) save(file string, content []byte) error {
	if err := r.assertPath(file); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*.tmp")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0600); err == nil {
		_, err = temporary.Write(content)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, file)
}
func (r relocation) saveJSON(file string, value protocol.JSONObject) error {
	raw, err := value.MarshalJSON()
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, raw, "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	return r.save(file, pretty.Bytes())
}
func (r relocation) rebase(raw json.RawMessage) json.RawMessage {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || !filepath.IsAbs(value) {
		return raw
	}
	clean := filepath.Clean(value)
	for _, root := range r.roots {
		if inside(root, clean) {
			rel, _ := filepath.Rel(root, clean)
			return encode(filepath.Join(r.target, rel))
		}
	}
	return raw
}
func stringField(value protocol.JSONObject, key string) bool {
	raw := value.Get(key)
	if len(raw) == 0 || raw[0] != '"' {
		return false
	}
	var s string
	return json.Unmarshal(raw, &s) == nil
}
func stringsField(value protocol.JSONObject, key string) ([]string, error) {
	raw := value.Get(key)
	var values []string
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return nil, errors.New("Invalid connector installation paths")
	}
	return values, nil
}
func ownEntries(value protocol.JSONObject) []struct {
	key string
	raw json.RawMessage
} {
	out := []struct {
		key string
		raw json.RawMessage
	}{}
	for _, field := range value.OwnProperties() {
		var key string
		_ = json.Unmarshal(field.NameJSON, &key)
		out = append(out, struct {
			key string
			raw json.RawMessage
		}{key, field.Value})
	}
	return out
}

// RelocateManagedData preserves unknown metadata fields and never edits source
// files, settings or transcript bytes. The host owns copy/verify/publication.
func RelocateManagedData(ctx context.Context, source, target, stage string) error {
	var err error
	source, err = filepath.Abs(source)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	stage, err = filepath.Abs(stage)
	if err != nil {
		return err
	}
	r := relocation{source: source, target: target, stage: stage, roots: []string{source}}
	canonical, err := filepath.EvalSymlinks(source)
	if err == nil {
		if canonical != source {
			r.roots = append(r.roots, canonical)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mappingPath := filepath.Join(stage, "cc-haha", "storage-relocations.json")
	mapping, err := r.read(mappingPath)
	if err != nil {
		return err
	}
	if mapping == nil {
		v := protocol.NewJSONObject(protocol.JSONField{Name: "version", Value: encode(1)}, protocol.JSONField{Name: "previousRoots", Value: encode([]string{})})
		mapping = &v
	}
	var version float64
	var roots []string
	if json.Unmarshal(mapping.Get("version"), &version) != nil || version != 1 || json.Unmarshal(mapping.Get("previousRoots"), &roots) != nil || roots == nil {
		return errors.New("Unsupported or invalid storage relocation metadata")
	}
	previous := []string{}
	for _, root := range append(roots, r.roots...) {
		if !filepath.IsAbs(root) || strings.ContainsRune(root, 0) {
			return errors.New("Unsupported or invalid storage relocation metadata")
		}
		root = filepath.Clean(root)
		if inside(target, root) || inside(root, target) {
			return errors.New("Previous storage root overlaps the migration target")
		}
		duplicate := false
		for _, old := range previous {
			if old == root || runtime.GOOS == "windows" && strings.EqualFold(old, root) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			previous = append(previous, root)
		}
	}
	catalog, err := connectors.DefaultCatalog()
	if err != nil {
		return err
	}
	definitions := map[string]protocol.JSONObject{}
	pluginDefinitions := map[string]protocol.JSONObject{}
	for _, def := range catalog.Definitions {
		definitions[def.String("id")] = def
		pluginDefinitions[def.String("pluginId")] = def
	}
	type managedInstallation struct {
		record  protocol.JSONObject
		version string
	}
	installations := map[string]managedInstallation{}
	connectorPath := filepath.Join(stage, "connectors", "state.json")
	state, err := r.read(connectorPath)
	if err != nil {
		return err
	}
	if state != nil {
		schema := state.Get("schemaVersion")
		if schema != nil && !versionAllowed(schema, 0, 1) {
			return errors.New("Unsupported connector state version")
		}
		records, err := object(state.Get("connectors"), "connector records")
		if err != nil {
			return err
		}
		for _, entry := range ownEntries(records) {
			record, err := object(entry.raw, "connector record")
			if err != nil {
				return err
			}
			raw := record.Get("installation")
			if raw == nil || string(raw) == "null" || string(raw) == "false" || string(raw) == "0" || string(raw) == `""` {
				continue
			}
			install, err := object(raw, "connector installation")
			if err != nil {
				return err
			}
			args, err := stringsField(install, "args")
			if err != nil || !stringField(install, "directory") || !stringField(install, "command") {
				return errors.New("Invalid connector installation paths")
			}
			env, err := object(install.Get("env"), "connector installation environment")
			if err != nil {
				return err
			}
			install.Set("directory", r.rebase(install.Get("directory")))
			install.Set("command", r.rebase(install.Get("command")))
			mapped := []json.RawMessage{}
			for _, arg := range args {
				mapped = append(mapped, r.rebase(encode(arg)))
			}
			install.Set("args", encode(mapped))
			for _, key := range []string{"DWS_CONFIG_DIR", "DWS_KEYCHAIN_DIR"} {
				if env.Get(key) != nil {
					if !stringField(env, key) {
						return errors.New("Invalid connector account path")
					}
					env.Set(key, r.rebase(env.Get(key)))
				}
			}
			install.Set("env", encode(env))
			record.Set("installation", encode(install))
			records.Set(entry.key, encode(record))
			installations[entry.key] = managedInstallation{install, record.String("installedVersion")}
		}
		state.Set("connectors", encode(records))
		if err = r.saveJSON(connectorPath, *state); err != nil {
			return err
		}
	}
	owned := map[string]string{}
	ownedOrder := []string{}
	addOwned := func(path, id string) {
		if _, ok := owned[path]; !ok {
			ownedOrder = append(ownedOrder, path)
		}
		owned[path] = id
	}
	for _, folder := range []string{"plugins", "cowork_plugins"} {
		if err = ctx.Err(); err != nil {
			return err
		}
		marketPath := filepath.Join(stage, folder, "known_marketplaces.json")
		markets, err := r.read(marketPath)
		if err != nil {
			return err
		}
		if markets != nil {
			for _, entry := range ownEntries(*markets) {
				value, err := object(entry.raw, "marketplace")
				if err != nil {
					return err
				}
				if !stringField(value, "installLocation") {
					return errors.New("Invalid marketplace install location")
				}
				value.Set("installLocation", r.rebase(value.Get("installLocation")))
				source, err := object(value.Get("source"), "marketplace source")
				if err != nil {
					return err
				}
				if source.String("source") == "file" || source.String("source") == "directory" {
					if !stringField(source, "path") {
						return errors.New("Invalid local marketplace source")
					}
					source.Set("path", r.rebase(source.Get("path")))
				}
				value.Set("source", encode(source))
				markets.Set(entry.key, encode(value))
			}
			if err = r.saveJSON(marketPath, *markets); err != nil {
				return err
			}
		}
		for _, filename := range []string{"installed_plugins.json", "installed_plugins_v2.json"} {
			pluginPath := filepath.Join(stage, folder, filename)
			plugins, err := r.read(pluginPath)
			if err != nil {
				return err
			}
			if plugins == nil {
				continue
			}
			version := plugins.Get("version")
			if version != nil && !versionAllowed(version, 1, 2) {
				return errors.New("Unsupported installed plugins version")
			}
			records, err := object(plugins.Get("plugins"), "installed plugins")
			if err != nil {
				return err
			}
			for _, entry := range ownEntries(records) {
				items := []json.RawMessage{entry.raw}
				array := len(entry.raw) > 0 && entry.raw[0] == '['
				if array && json.Unmarshal(entry.raw, &items) != nil {
					return errors.New("Invalid installed plugin")
				}
				updated := []protocol.JSONObject{}
				for _, raw := range items {
					value, err := object(raw, "installed plugin")
					if err != nil {
						return err
					}
					if !stringField(value, "installPath") {
						return errors.New("Invalid installed plugin path")
					}
					value.Set("installPath", r.rebase(value.Get("installPath")))
					updated = append(updated, value)
					if def, ok := pluginDefinitions[entry.key]; ok && inside(target, filepath.Clean(value.String("installPath"))) {
						addOwned(filepath.Join(value.String("installPath"), "skills", "office-"+def.String("id"), "SKILL.md"), def.String("id"))
					}
				}
				if array {
					records.Set(entry.key, encode(updated))
				} else {
					records.Set(entry.key, encode(updated[0]))
				}
			}
			plugins.Set("plugins", encode(records))
			if err = r.saveJSON(pluginPath, *plugins); err != nil {
				return err
			}
		}
	}
	for _, def := range catalog.Definitions {
		addOwned(filepath.Join(target, "connectors", "marketplace", "plugins", "office-"+def.String("id"), "skills", "office-"+def.String("id"), "SKILL.md"), def.String("id"))
	}
	versionFromDir := regexp.MustCompile(`^(.+)-(?:darwin|win32|linux)-(?:x64|arm64)$`)
	for _, final := range ownedOrder {
		if err = ctx.Err(); err != nil {
			return err
		}
		id := owned[final]
		managed, ok := installations[id]
		if !ok || !inside(filepath.Join(target, "connectors"), filepath.Clean(managed.record.String("directory"))) {
			continue
		}
		if !inside(target, filepath.Clean(final)) {
			return errors.New("Managed connector path escaped migration target")
		}
		relative, _ := filepath.Rel(target, final)
		file := filepath.Join(stage, relative)
		if err = r.assertPath(file); err != nil {
			return err
		}
		if _, err = os.Lstat(file); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		def := definitions[id].Clone()
		version := managed.version
		if version == "" {
			if match := versionFromDir.FindStringSubmatch(filepath.Base(managed.record.String("directory"))); len(match) > 1 {
				version = match[1]
			} else {
				version = def.String("version")
			}
		}
		def.Set("version", encode(version))
		if id != "feishu" && id != "dingtalk" && id != "wecom" {
			return errors.New("Invalid managed native connector identity")
		}
		installation := connectors.Installation{Directory: managed.record.String("directory"), Command: managed.record.String("command"), Args: []string{}, Environment: map[string]string{}, EnvironmentOrder: []string{}}
		if err = json.Unmarshal(managed.record.Get("args"), &installation.Args); err != nil {
			return err
		}
		environment, err := object(managed.record.Get("env"), "connector installation environment")
		if err != nil {
			return err
		}
		for _, entry := range ownEntries(environment) {
			if entry.key != "DWS_CONFIG_DIR" && entry.key != "DWS_KEYCHAIN_DIR" && entry.key != "DWS_DISABLE_KEYCHAIN" {
				continue
			}
			if !stringField(environment, entry.key) {
				return errors.New("Invalid connector installation environment")
			}
			installation.Environment[entry.key] = environment.String(entry.key)
			installation.EnvironmentOrder = append(installation.EnvironmentOrder, entry.key)
		}
		text, err := connectors.RenderConnectorSkill(connectors.Catalog{}, def, installation)
		if err != nil {
			return err
		}
		if err = r.save(file, []byte(text)); err != nil {
			return err
		}
	}
	mapping.Set("version", encode(1))
	mapping.Set("previousRoots", encode(previous))
	return r.saveJSON(mappingPath, *mapping)
}

func versionAllowed(raw json.RawMessage, allowed ...float64) bool {
	var value float64
	if json.Unmarshal(raw, &value) != nil || len(raw) == 0 || raw[0] == 'n' {
		return false
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
