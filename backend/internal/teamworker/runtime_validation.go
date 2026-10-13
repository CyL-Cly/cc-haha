package teamworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type ValidationOptions struct {
	Config         *teamstore.Config
	Plans          *teamplan.Store
	PermissionMode func(context.Context, string) (string, error)
	// Resolves the actual saved provider/official concrete model and reasoning
	// metadata locally. Validation never refreshes credentials or calls a model.
	Routing func(context.Context, protocol.JSONObject, string) (protocol.JSONObject, string, error)
}

// JavaScript's \s includes BOM but excludes the Unicode NEL character.
func concreteModelID(model string) bool {
	return model != "" && model != "inherit" && strings.IndexFunc(model, func(r rune) bool {
		return r <= 0x1f || r == 0xfeff || r != 0x85 && unicode.IsSpace(r)
	}) < 0
}

func ValidateRuntime(ctx context.Context, plan protocol.JSONObject, o ValidationOptions) (protocol.JSONObject, error) {
	if o.Config == nil || o.Plans == nil || o.PermissionMode == nil || o.Routing == nil {
		return protocol.JSONObject{}, errors.New("Team runtime requires actual configuration, routing and leader permissions")
	}
	info, err := os.Stat(plan.String("workDir"))
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if !info.IsDir() {
		return protocol.JSONObject{}, errors.New("Team working directory is unavailable")
	}
	team := o.Config.Read(plan.String("teamName"))
	if plan.String("teamName") != "" {
		incarnation, err := o.Plans.Incarnation(plan.String("teamName"), plan.String("sessionId"))
		if err != nil || team == nil || team.String("leadSessionId") != plan.String("sessionId") || incarnation != plan.String("incarnationId") {
			return protocol.JSONObject{}, errors.New("Team generation no longer exists")
		}
	}
	var members, existing []protocol.JSONObject
	_ = json.Unmarshal(plan.Get("members"), &members)
	if team != nil {
		_ = json.Unmarshal(team.Get("members"), &existing)
	}
	var catalog protocol.JSONObject
	_ = catalog.UnmarshalJSON(plan.Get("agentCatalog"))
	names := map[string]bool{}
	for i, member := range members {
		name := member.String("name")
		if !teamplan.ValidMemberName(name) {
			return protocol.JSONObject{}, fmt.Errorf("Invalid teammate name: %s", name)
		}
		for _, entry := range existing {
			if entry.String("name") == name {
				return protocol.JSONObject{}, fmt.Errorf("Member already exists: %s. Message the existing member to give it more work; a stopped member restarts from its saved conversation.", name)
			}
		}
		var snapshot protocol.JSONObject
		_ = snapshot.UnmarshalJSON(catalog.Get(member.String("agentType")))
		if !snapshot.IsObject() || protocol.TrimJavaScriptString(snapshot.String("systemPrompt")) == "" {
			return protocol.JSONObject{}, fmt.Errorf("Agent preset is unavailable: %s", member.String("agentType"))
		}
		if err := ValidatePresetSource(snapshot); err != nil {
			return protocol.JSONObject{}, err
		}
		if configuration := snapshot.String("configurationError"); configuration != "" {
			return protocol.JSONObject{}, fmt.Errorf("Agent preset %s: %s", member.String("agentType"), configuration)
		}
		if required := snapshot.String("permissionMode"); required != "" && required != "default" {
			mode, err := o.PermissionMode(ctx, plan.String("sessionId"))
			if err != nil {
				return protocol.JSONObject{}, err
			}
			if required != mode {
				return protocol.JSONObject{}, fmt.Errorf("Agent preset %s requires permission mode %s; adjust the preset or leader permission mode and submit a new plan", member.String("agentType"), required)
			}
		}
		if value := snapshot.String("isolation"); value != "" {
			return protocol.JSONObject{}, fmt.Errorf("Agent preset %s uses unsupported team worker settings: isolation", member.String("agentType"))
		}
		if value := snapshot.Get("maxTurns"); value != nil {
			var n float64
			if json.Unmarshal(value, &n) != nil || n < 1 || math.Trunc(n) != n {
				return protocol.JSONObject{}, fmt.Errorf("Agent preset %s has invalid maxTurns", member.String("agentType"))
			}
		}
		var runtime protocol.JSONObject
		_ = runtime.UnmarshalJSON(member.Get("runtime"))
		resolved, providerName, err := o.Routing(ctx, runtime, name)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		model := resolved.String("modelId")
		if !concreteModelID(model) {
			return protocol.JSONObject{}, fmt.Errorf("Choose a concrete model for %s", name)
		}
		var denied []string
		_ = json.Unmarshal(snapshot.Get("disallowedTools"), &denied)
		for _, rule := range denied {
			tool := strings.SplitN(rule, "(", 2)[0]
			for _, essential := range EssentialTools {
				if tool == essential {
					return protocol.JSONObject{}, fmt.Errorf("Agent preset %s disables team communication tools", member.String("agentType"))
				}
			}
		}
		next := member.Clone()
		next.Set("providerName", raw(providerName))
		next.Set("runtime", raw(resolved))
		next.Set("agentSnapshot", raw(snapshot.Clone()))
		members[i] = next
		names[name] = true
	}
	if len(names) != len(members) {
		return protocol.JSONObject{}, errors.New("Teammate names must be unique")
	}
	next := plan.Clone()
	next.Set("members", raw(members))
	return next, nil
}
