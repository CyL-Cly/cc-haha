// This executable is test-only and requires the parent's disposable HOME.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/h5access"
	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type network struct {
	Name    string `json:"name"`
	Entries []struct {
		Address  string          `json:"address"`
		Family   json.RawMessage `json:"family"`
		Internal bool            `json:"internal"`
	} `json:"entries"`
}
type step struct {
	Operation   string                     `json:"operation"`
	Value       json.RawMessage            `json:"value"`
	Patch       storage.Object             `json:"patch"`
	Unset       []string                   `json:"unset"`
	TokenFrom   *int                       `json:"tokenFrom"`
	Environment map[string]*string         `json:"environment"`
	Interfaces  *[]network                 `json:"interfaces"`
	ActivePort  *int                       `json:"activePort"`
	Hosts       *[]string                  `json:"hosts"`
	Effective   h5access.EffectiveURLInput `json:"effective"`
}
type request struct {
	HomeDir     string             `json:"homeDir"`
	Interfaces  []network          `json:"interfaces"`
	ActivePort  int                `json:"activePort"`
	Environment map[string]*string `json:"environment"`
	Steps       []step             `json:"steps"`
}
type result struct {
	Value any          `json:"value,omitempty"`
	Error *errorResult `json:"error,omitempty"`
}

// bool false, numeric zero and null must keep the value key in success.
func (value result) MarshalJSON() ([]byte, error) {
	if value.Error != nil {
		return json.Marshal(struct {
			Error *errorResult `json:"error"`
		}{value.Error})
	}
	return json.Marshal(struct {
		Value any `json:"value"`
	}{value.Value})
}

type errorResult struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type output struct {
	Results        []result         `json:"results"`
	Effects        []map[string]any `json:"effects"`
	RandomCalls    []int            `json:"randomCalls"`
	InterfaceCalls int              `json:"interfaceCalls"`
	PortCalls      int              `json:"portCalls"`
	WarningCount   int              `json:"warningCount"`
	Snapshots      []map[string]any `json:"snapshots"`
}

type deterministicRandom struct {
	output     *output
	generation int
}

func (random *deterministicRandom) Read(data []byte) (int, error) {
	random.output.RandomCalls = append(random.output.RandomCalls, len(data))
	if len(data) == 32 {
		random.generation++
		for index := range data {
			data[index] = byte(index + random.generation*17)
		}
	} else {
		for index := range data {
			data[index] = byte(index%3 + 1)
		}
	}
	return len(data), nil
}

type fileSystem struct {
	home   string
	output *output
}

func (filesystem fileSystem) relative(path string) (string, error) {
	name, err := filepath.Rel(filesystem.home, path)
	if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) || filepath.IsAbs(name) {
		return "", fmt.Errorf("H5 fixture path escaped HOME: %s", path)
	}
	return filepath.ToSlash(name), nil
}
func (filesystem fileSystem) ReadFile(path string) ([]byte, error) {
	if _, err := filesystem.relative(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
func (filesystem fileSystem) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if _, err := filesystem.relative(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
func (filesystem fileSystem) MkdirAll(path string, mode fs.FileMode) error {
	if _, err := filesystem.relative(path); err != nil {
		return err
	}
	return os.MkdirAll(path, mode)
}
func (filesystem fileSystem) Rename(from, to string) error {
	if _, err := filesystem.relative(from); err != nil {
		return err
	}
	name, err := filesystem.relative(to)
	if err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		return err
	}
	if name == ".claude/cc-haha/settings.json" {
		raw, err := filesystem.ReadFile(to)
		if err != nil {
			return err
		}
		value, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
		if err != nil {
			return err
		}
		filesystem.output.Effects = append(filesystem.output.Effects, map[string]any{"operation": "write", "path": name, "value": json.RawMessage(value)})
	}
	return nil
}
func (filesystem fileSystem) Remove(path string) error {
	if _, err := filesystem.relative(path); err != nil {
		return err
	}
	return os.Remove(path)
}
func (filesystem fileSystem) CopyFile(from, to string) error {
	raw, err := filesystem.ReadFile(from)
	if err != nil {
		return err
	}
	return filesystem.WriteFile(to, raw, 0666)
}
func (filesystem fileSystem) snapshot() (map[string]any, error) {
	files := map[string]any{}
	err := filepath.WalkDir(filepath.Join(filesystem.home, ".claude"), func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("H5 fixture snapshot cannot follow a symlink")
		}
		name, err := filesystem.relative(path)
		if err != nil {
			return err
		}
		raw, err := filesystem.ReadFile(path)
		if err != nil {
			return err
		}
		parsed, err := protocol.ParseAndStringifyJavaScriptJSON(storage.DecodeUTF8(raw))
		if err == nil {
			files[name] = map[string]any{"json": json.RawMessage(parsed)}
		} else {
			files[name] = map[string]any{"raw": string(storage.DecodeUTF8(raw))}
		}
		return nil
	})
	return files, err
}

func interfaceValues(input []network) []h5access.NetworkInterface {
	// Object.fromEntries overwrites duplicate values without moving the first
	// insertion, then JS enumeration lists canonical array-index keys first.
	values := []network{}
	indices := map[string]int{}
	for _, value := range input {
		if index, ok := indices[value.Name]; ok {
			values[index] = value
		} else {
			indices[value.Name] = len(values)
			values = append(values, value)
		}
	}
	arrayIndex := func(value string) (uint64, bool) {
		index, err := strconv.ParseUint(value, 10, 32)
		return index, err == nil && index < 4294967295 && strconv.FormatUint(index, 10) == value
	}
	sort.SliceStable(values, func(i, j int) bool {
		left, leftOK := arrayIndex(values[i].Name)
		right, rightOK := arrayIndex(values[j].Name)
		if leftOK && rightOK {
			return left < right
		}
		return leftOK && !rightOK
	})
	result := []h5access.NetworkInterface{}
	for _, value := range values {
		addresses := []h5access.InterfaceAddress{}
		for _, entry := range value.Entries {
			var family string
			_ = json.Unmarshal(entry.Family, &family)
			addresses = append(addresses, h5access.InterfaceAddress{Address: entry.Address, Family: family, Internal: entry.Internal})
		}
		result = append(result, h5access.NetworkInterface{Name: value.Name, Addresses: addresses})
	}
	return result
}

func stringValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func run() error {
	var input request
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		return err
	}
	if input.HomeDir == "" || !filepath.IsAbs(input.HomeDir) || input.HomeDir != os.Getenv("HOME") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.HomeDir, ".claude") {
		return errors.New("H5 service fixture requires explicit disposable HOME/config")
	}
	out := output{Results: []result{}, Effects: []map[string]any{}, RandomCalls: []int{}, Snapshots: []map[string]any{}}
	environment := map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(input.HomeDir, ".claude")}
	updateEnvironment := func(patch map[string]*string) {
		for key, value := range patch {
			if value == nil {
				delete(environment, key)
			} else {
				environment[key] = *value
			}
		}
	}
	updateEnvironment(input.Environment)
	interfaces := input.Interfaces
	port := input.ActivePort
	random := &deterministicRandom{output: &out}
	filesystem := fileSystem{home: input.HomeDir, output: &out}
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	store := storage.New(storage.Options{FS: filesystem, Now: now, Random: random, Warn: func(string) { out.WarningCount++ }})
	upgrader, err := migrations.New(migrations.Options{Store: store, Now: now, Random: random, Access: func(path string) error {
		if _, err := filesystem.relative(path); err != nil {
			return err
		}
		_, err := os.Stat(path)
		return err
	}})
	if err != nil {
		return err
	}
	managed, err := settings.NewManaged(settings.Options{Store: store, HomeDir: input.HomeDir, Environment: func() map[string]string { return environment }, EnsureMigrated: func(ctx context.Context, config string) error {
		name, err := filesystem.relative(config)
		if err != nil {
			return err
		}
		out.Effects = append(out.Effects, map[string]any{"operation": "migration", "config": name})
		return upgrader.SettingsCallback(ctx, config)
	}})
	if err != nil {
		return err
	}
	getInterfaces := func() []h5access.NetworkInterface { out.InterfaceCalls++; return interfaceValues(interfaces) }
	service, err := h5access.New(h5access.Options{Managed: managed, Environment: func() map[string]string { return environment }, NetworkInterfaces: getInterfaces, ServerPort: func() int { out.PortCalls++; return port }, Random: random})
	if err != nil {
		return err
	}
	ctx := context.Background()
	for _, step := range input.Steps {
		updateEnvironment(step.Environment)
		if step.Interfaces != nil {
			interfaces = *step.Interfaces
		}
		if step.ActivePort != nil {
			port = *step.ActivePort
		}
		var value any
		var callErr error
		switch step.Operation {
		case "read":
			value, callErr = service.GetSettings(ctx)
		case "update":
			for _, key := range step.Unset {
				delete(step.Patch, key)
			}
			value, callErr = service.UpdateSettings(ctx, step.Patch)
		case "enable":
			value, callErr = service.Enable(ctx)
		case "disable":
			value, callErr = service.Disable(ctx)
		case "regenerate":
			value, callErr = service.RegenerateToken(ctx)
		case "validate-token":
			token := stringValue(step.Value)
			if step.TokenFrom != nil {
				index := *step.TokenFrom
				if index < 0 || index >= len(out.Results) {
					return fmt.Errorf("Invalid tokenFrom fixture step: %d", index)
				}
				enabled, ok := out.Results[index].Value.(h5access.EnableResult)
				if !ok {
					return fmt.Errorf("Invalid tokenFrom fixture step: %d", index)
				}
				token = enabled.Token
			}
			value, callErr = service.ValidateToken(ctx, token)
		case "origin":
			value, callErr = service.IsOriginAllowed(ctx, stringValue(step.Value))
		case "diagnostics":
			value, callErr = service.GetDiagnostics(ctx)
		case "grace":
			value, callErr = service.GetDisconnectGraceMS(ctx)
		case "classify-url":
			value = h5access.ClassifyPublicBaseURL(stringValue(step.Value))
		case "validate-url":
			hosts := []string{}
			if step.Hosts == nil {
				hosts = h5access.CollectLocalIPv4Hosts(getInterfaces())
			} else {
				hosts = *step.Hosts
			}
			value = h5access.ValidatePublicBaseURL(stringValue(step.Value), hosts)
		case "collect-hosts":
			value = h5access.CollectLocalIPv4Hosts(getInterfaces())
		case "find-lan":
			value = h5access.FindPrivateLanAddress(getInterfaces())
		case "effective-url":
			value = h5access.ResolveEffectivePublicBaseURL(step.Effective)
		case "browser-safe-port":
			var number float64
			if json.Unmarshal(step.Value, &number) != nil {
				return errors.New("invalid browser port fixture")
			}
			value = h5access.IsBrowserSafePort(number)
		default:
			return fmt.Errorf("Unknown H5 service fixture operation: %s", step.Operation)
		}
		if callErr != nil {
			failure := &errorResult{Status: 500, Code: "INTERNAL_ERROR", Message: callErr.Error()}
			var apiError *settings.APIError
			if errors.As(callErr, &apiError) {
				failure.Status = apiError.StatusCode
				failure.Code = apiError.Code
			}
			out.Results = append(out.Results, result{Error: failure})
		} else {
			out.Results = append(out.Results, result{Value: value})
		}
		snapshot, err := filesystem.snapshot()
		if err != nil {
			return err
		}
		out.Snapshots = append(out.Snapshots, snapshot)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(out)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
