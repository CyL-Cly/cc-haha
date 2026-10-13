package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelselection"
	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

type nativeRemoteHydration struct {
	Metadata    []byte
	HasMessages bool
}

func (m *MainPrint) hydrateNativeRemoteSession(scope *cooperative.Scope) error {
	m.prepareNativeResumeIdentifier()
	if m.remoteIO == nil || m.remoteIO.Kind() != remoteio.CCR {
		return m.hydrateNativeLegacyIngress(scope)
	}
	if m.options.Arguments.Resume == "" {
		return nil
	}
	sid := m.nativeResumeParsedSID
	if !printResumeUUID.MatchString(sid) {
		return nil
	}
	remote, n := m.remoteIO, m.options.Native
	config := n.Environment["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(n.Home, ".claude")
	}
	project := filepath.Join(config, "projects", projectpath.Sanitize(m.state.Snapshot().OriginalCWD))
	files := mainResumeFiles{n: n}
	future, err := nativeSkillWork(m, scope, func() (nativeRemoteHydration, error) {
		// Source restores worker metadata concurrently with foreground history,
		// then writes foreground before fetching/writing subagent transcripts.
		foreground, readErr := remote.ReadInternalEvents(context.Background(), false)
		if readErr != nil {
			return nativeRemoteHydration{}, readErr
		}
		if foreground != nil {
			if err := files.MkdirAll(project); err != nil {
				return nativeRemoteHydration{}, err
			}
			var content bytes.Buffer
			for _, event := range foreground {
				encoded, e := event.Payload.MarshalJSON()
				if e != nil {
					return nativeRemoteHydration{}, e
				}
				content.Write(protocol.StringifyValidatedJavaScriptJSON(encoded))
				content.WriteByte('\n')
			}
			if err := files.WriteFile(filepath.Join(project, sid+".jsonl"), content.Bytes()); err != nil {
				return nativeRemoteHydration{}, err
			}
			subagents, e := remote.ReadInternalEvents(context.Background(), true)
			if e != nil {
				return nativeRemoteHydration{}, e
			}
			byAgent := map[string]*bytes.Buffer{}
			for _, event := range subagents {
				id := event.AgentID
				if id == "" {
					continue
				}
				if strings.ContainsAny(id, "/\\\x00") || id == "." || id == ".." {
					return nativeRemoteHydration{}, errors.New("invalid remote agent transcript identity")
				}
				if byAgent[id] == nil {
					byAgent[id] = &bytes.Buffer{}
				}
				encoded, e := event.Payload.MarshalJSON()
				if e != nil {
					return nativeRemoteHydration{}, e
				}
				byAgent[id].Write(protocol.StringifyValidatedJavaScriptJSON(encoded))
				byAgent[id].WriteByte('\n')
			}
			for id, content := range byAgent {
				path := filepath.Join(project, sid, "subagents", "agent-"+id+".jsonl")
				if err := files.MkdirAll(filepath.Dir(path)); err != nil {
					return nativeRemoteHydration{}, err
				}
				if err := files.WriteFile(path, content.Bytes()); err != nil {
					return nativeRemoteHydration{}, err
				}
			}
		}
		metadata, e := remote.RestoredWorkerState(context.Background())
		if e != nil {
			return nativeRemoteHydration{}, e
		}
		encoded, _ := metadata.MarshalJSON()
		return nativeRemoteHydration{Metadata: encoded, HasMessages: len(foreground) > 0}, nil
	})
	if err != nil {
		return err
	}
	hydrated, err := future.Await(scope)
	if err != nil {
		return err
	}
	return m.applyNativeRestoredMetadataOnOwner(scope, nativeInProcessObject(hydrated.Metadata))
}

func (m *MainPrint) applyNativeRestoredMetadataOnOwner(scope *cooperative.Scope, metadata protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || m.Host == nil {
		return cooperative.ErrRestricted
	}
	if string(metadata.Get("is_ultraplan_mode")) == "true" || string(metadata.Get("is_ultraplan_mode")) == "false" {
		m.nativeUltraplan = string(metadata.Get("is_ultraplan_mode")) == "true"
	}
	var model string
	if value := metadata.Get("model"); len(value) > 0 && json.Unmarshal(value, &model) == nil && value[0] == '"' {
		settings, err := m.Startup.GetInitialSettings()
		if err != nil {
			return err
		}
		merged := protocol.NewJSONObject()
		if settings != nil {
			merged = settings.Clone()
		}
		selector, err := m.nativeModelSelection(context.Background(), scope, modelselection.Options{Environment: m.nativeOpenAIEnvironment(), Settings: merged, Override: quoteMain(model)})
		if err != nil {
			return err
		}
		resolved, selector, err := selectNativeModel(selector)
		if err != nil {
			return err
		}
		if err := m.Host.RestoreNativeRemoteInferenceModel(scope, resolved); err != nil {
			return err
		}
		m.nativeModelSelector = selector
		m.options.Arguments.Model = model
	}
	var mode string
	if value := metadata.Get("permission_mode"); len(value) > 0 && json.Unmarshal(value, &mode) == nil && value[0] == '"' {
		if err := m.Host.RestoreNativeRemotePermissionMode(scope, mode); err != nil {
			return err
		}
	}
	return nil
}
