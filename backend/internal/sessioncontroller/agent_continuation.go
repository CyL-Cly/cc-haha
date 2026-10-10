package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"path/filepath"
	"strings"
)

func (c *Controller) AgentTranscriptSourceOnOwner(scope *cooperative.Scope, id string) (string, *sessionhistory.Loader, *sessionresume.Reducer, error) {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || id == "" || strings.ContainsAny(id, "/\\\x00") {
		return "", nil, nil, cooperative.ErrRestricted
	}
	c.writer.mu.Lock()
	defer c.writer.mu.Unlock()
	snapshot := c.options.Bootstrap.Snapshot()
	project := filepath.Join(c.writer.options.ConfigDir, "projects", sanitize(snapshot.OriginalCWD))
	if snapshot.SessionProjectDir != nil {
		project = *snapshot.SessionProjectDir
	}
	return filepath.Join(project, c.SessionID(), "subagents", "agent-"+id+".jsonl"), c.options.ResumeOptions.Loader, c.options.ResumeOptions.Reducer, nil
}

func (c *Controller) RegisterAgentNameOnOwner(scope *cooperative.Scope, name, id string) error {
	root, err := c.todosController(scope)
	if err != nil {
		return err
	}
	if root.appState.AgentNameRegistry == nil {
		root.appState.AgentNameRegistry = map[string]string{}
	}
	root.appState.AgentNameRegistry[name] = id
	return nil
}
func (c *Controller) ResolveAgentNameOnOwner(scope *cooperative.Scope, name string) (string, error) {
	root, err := c.todosController(scope)
	if err != nil {
		return "", err
	}
	return root.appState.AgentNameRegistry[name], nil
}

// InstallAgentResumeHistory accepts recovered original envelopes only before
// this actual child starts ASK. Record uses the original UUID file cache, so
// already persisted messages are not written a second time.
func (c *Controller) InstallAgentResumeHistory(scope *cooperative.Scope, history []protocol.JSONObject) error {
	if c == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || c.options.WriterOptions.AgentID == "" || len(c.messages) != 0 || len(c.queue) != 0 {
		return cooperative.ErrRestricted
	}
	for _, row := range history {
		if !row.IsObject() || row.String("uuid") == "" {
			return capability("Agent resume original envelope UUID")
		}
		copy := row.Clone()
		c.messages = append(c.messages, &copy)
	}
	return nil
}

func MergeAgentReplacement(previous, incoming protocol.JSONObject) protocol.JSONObject {
	out := incoming.Clone()
	if previous.Get("retain") == nil {
		return out
	}
	if value := previous.Get("toolUseId"); value != nil && string(value) != "null" {
		out.Set("toolUseId", value)
	}
	for _, key := range []string{"retain", "startTime", "messages", "diskLoaded", "pendingMessages"} {
		if value := previous.Get(key); value != nil {
			out.Set(key, value)
		} else {
			out.Delete(key)
		}
	}
	if previous.String("type") == "local_agent" && incoming.String("type") == "local_agent" {
		if value := incoming.Get("ownerAgentId"); value == nil || string(value) == "null" {
			out.Set("ownerAgentId", previous.Get("ownerAgentId"))
		}
	} else if value := previous.Get("ownerAgentId"); value != nil {
		out.Set("ownerAgentId", value)
	}
	return out
}
