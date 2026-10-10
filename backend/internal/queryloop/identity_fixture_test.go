package queryloop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
	"time"
)

func TestToolIdentityFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_TOOL_IDENTITY_FIXTURE") != "1" {
		t.Skip("explicit disposable tool identity fixture only")
	}
	var input identityFixtureInput
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("NODE_ENV") != "production" || os.Getenv("BUN_OPTIONS") != "--no-env-file" || input.Home == "" || !filepath.IsAbs(input.Home) || os.Getenv("HOME") != input.Home || os.Getenv("USERPROFILE") != input.Home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		t.Fatal("tool identity requires private HOME/config")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	if err != nil || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		t.Fatal("tool identity requires trusted allocation root")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd, err = filepath.EvalSymlinks(cwd)
	if err != nil || cwd != home {
		t.Fatal("tool identity requires private cwd")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			t.Fatal("tool identity requires regular private directories")
		}
	}
	if input.Registry.Name == "" || len(input.UsesSources) == 0 {
		t.Fatal("tool identity requires original registry/model inputs")
	}
	fmt.Println("TOOL_IDENTITY_RESULT:" + string(runIdentityFixture(t, input, home)))
}

// Fixture registry inputs declare the same scalar profile as production.
// Validate quoted code units before encoding/json can replace a surrogate.
type identityRegistry struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

func (registry *identityRegistry) UnmarshalJSON(raw []byte) error {
	var object protocol.JSONObject
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	name, err := modelExecutionString(object.Get("name"), "registry name")
	if err != nil {
		return err
	}
	rawAliases := bytes.TrimSpace(object.Get("aliases"))
	if len(rawAliases) == 0 || rawAliases[0] != '[' {
		return errors.New("identity fixture registry requires an explicit alias array")
	}
	var aliases []json.RawMessage
	if err := json.Unmarshal(rawAliases, &aliases); err != nil {
		return err
	}
	result := identityRegistry{Name: name, Aliases: []string{}}
	for _, raw := range aliases {
		alias, err := modelExecutionString(raw, "registry alias")
		if err != nil {
			return err
		}
		result.Aliases = append(result.Aliases, alias)
	}
	*registry = result
	return nil
}

type identityFixtureInput struct {
	Name              string           `json:"name"`
	Aborted           bool             `json:"aborted"`
	Home              string           `json:"home"`
	Registry          identityRegistry `json:"registry"`
	UsesSources       []string         `json:"usesSources"`
	DeprecatedCatalog []struct {
		Name    string   `json:"name"`
		Aliases []string `json:"aliases"`
	} `json:"deprecatedCatalog"`
}

func runIdentityFixture(t *testing.T, input identityFixtureInput, home string) []byte {
	observation := struct {
		LookupNames []*string `json:"lookupNames"`
		Calls       []string  `json:"calls"`
		Mapped      []string  `json:"mapped"`
		File        *string   `json:"file"`
		Unsupported bool      `json:"unsupported"`
	}{LookupNames: []*string{}, Calls: []string{}, Mapped: []string{}}
	h := newBatchHarness(t)
	h.Tool.Name, h.Tool.Aliases = input.Registry.Name, input.Registry.Aliases
	h.Tool.ValidateInput = nil
	h.Uses = nil
	h.Tool.CallJSON = func(_ context.Context, _ protocol.JSONObject, call JSONToolCallContext) (ToolCallResult, error) {
		observation.Calls = append(observation.Calls, string(call.ToolUseID))
		raw, _ := json.Marshal(observation.Calls)
		return ToolCallResult{Data: quoted("written")}, os.WriteFile(filepath.Join(home, "identity.txt"), raw, 0600)
	}
	h.Tool.MapResultJSON = func(data json.RawMessage, id json.RawMessage) (protocol.JSONObject, error) {
		observation.Mapped = append(observation.Mapped, string(id))
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("tool_result")}, protocol.JSONField{Name: "tool_use_id", Value: id}, protocol.JSONField{Name: "content", Value: data}), nil
	}
	feedbackSources, progress := []string{}, []string{}
	backfills := 0
	h.Tool.Backfill = func(*protocol.JSONObject) error { backfills++; return nil }
	h.Factory, _ = messages.NewFactory(func() string { h.UUIDs++; return fmt.Sprintf("identity-%d", h.UUIDs) }, func() time.Time { return time.UnixMilli(1700000000000) })
	h.Capabilities.CancellationContent = quoted(cancelMessage)
	for _, d := range input.DeprecatedCatalog {
		h.Capabilities.DeprecatedCatalog = append(h.Capabilities.DeprecatedCatalog, ToolDescriptor{Name: d.Name, Aliases: d.Aliases})
	}
	ids := []json.RawMessage{}
	h.Capabilities.JSON = &JSONToolBatchCapabilities{
		PreTool: func(_ context.Context, _ RegisteredTool, v *protocol.JSONObject, _ JSONToolCallContext) (*protocol.JSONObject, error) {
			return v, nil
		},
		ResolvePermission: func(_ context.Context, _ RegisteredTool, v *protocol.JSONObject, _ JSONToolCallContext) (ToolPermission, error) {
			return ToolPermission{Behavior: "allow", Input: v}, nil
		},
		PostTool: func(context.Context, RegisteredTool, *protocol.JSONObject, ToolCallResult, JSONToolCallContext) ([]Message, error) {
			return nil, nil
		},
		InProgress: func(id json.RawMessage, active bool) error {
			if active {
				found := false
				for _, old := range ids {
					if equalString(old, id) {
						found = true
					}
				}
				if !found {
					ids = append(ids, id)
				}
			} else {
				next := []json.RawMessage{}
				for _, old := range ids {
					if !equalString(old, id) {
						next = append(next, old)
					}
				}
				ids = next
			}
			raw, _ := json.Marshal(ids)
			progress = append(progress, string(raw))
			return nil
		},
	}
	h.Emit = func(message Message) error {
		raw, err := message.MarshalJSON()
		feedbackSources = append(feedbackSources, string(raw))
		return err
	}
	batch, err := NewSerialToolBatch([]RegisteredTool{h.Tool}, h.Capabilities, h.Factory)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range input.UsesSources {
		block := &streamhistory.Block{Object: fixtureObject(source)}
		var name *string
		if modelName, err := modelJSONString(block.Object.Get("name"), "name"); err == nil {
			if tool := batch.findJSON(modelName); tool != nil {
				value := tool.Name
				name = &value
			}
		}
		observation.LookupNames = append(observation.LookupNames, name)
		h.Uses = append(h.Uses, Invocation{block, &streamhistory.Envelope{UUID: "original-assistant"}})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if input.Aborted {
		cancel()
	}
	_, err = batch.Execute(ctx, Round{}, h.Uses, h.Emit)
	observation.Unsupported = errors.Is(err, ErrUnsupported)
	if err != nil && !observation.Unsupported {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(home, "identity.txt")); err == nil {
		value := string(raw)
		observation.File = &value
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	effects := h.Order
	if effects == nil {
		effects = []string{}
	}
	raw, err := json.Marshal(struct {
		Observation any      `json:"observation"`
		Effects     []string `json:"effects"`
		SourceOnly  any      `json:"sourceOnly"`
	}{observation, effects, map[string]any{"feedbackSources": feedbackSources, "progress": progress, "backfills": backfills, "deprecatedCatalog": input.DeprecatedCatalog}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
