package sessioncontroller

import (
	"encoding/json"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
)

// TerminalResume is the ordinary idle REPL's original LogOption transition.
// Its fields cannot be supplied as prepared model history or startup state.
// It always belongs to the existing Controller, Writer and SerialASK graph.
type TerminalResume struct {
	controller *Controller
	log        protocol.JSONObject
	messages   []queryloop.Message
	recovered  *ReadCache
	stage      uint8
}

func (c *Controller) checkTerminalResume(scope *cooperative.Scope, value *TerminalResume) error {
	if c == nil || scope == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) || !c.active || c.activationPending || !c.options.PublicPrint || !c.options.NativeTools || !c.options.AsyncPersistence || !c.options.NativeInteractiveQueue || c.options.WriterOptions.AgentID != "" || value != nil && value.controller != c {
		return cooperative.ErrRestricted
	}
	return nil
}

// PrepareTerminalResumeOnOwner is REPL's first deserializeMessages(log.messages)
// call. It deliberately does not invoke headless loadInitialMessages: that
// producer restores plans/skills/hooks in a different order.
func (c *Controller) PrepareTerminalResumeOnOwner(scope *cooperative.Scope, log protocol.JSONObject) (*TerminalResume, error) {
	if err := c.checkTerminalResume(scope, nil); err != nil {
		return nil, err
	}
	if !log.IsObject() || log.String("sessionId") == "" {
		return nil, capability("actual terminal LogOption/session identity")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(log.Get("messages"), &rows); err != nil {
		return nil, err
	}
	parsed, err := sessionresume.ParseMessages(rows)
	if err != nil {
		return nil, err
	}
	result, err := c.options.ResumeOptions.Reducer.Deserialize(parsed)
	if err != nil {
		return nil, err
	}
	value := &TerminalResume{controller: c, log: log.Clone(), messages: []queryloop.Message{}}
	for _, item := range result.Messages {
		var record protocol.JSONObject
		if err := json.Unmarshal(raw(item), &record); err != nil {
			return nil, err
		}
		value.messages = append(value.messages, queryloop.Message{Record: record})
	}
	return value, nil
}

func (c *Controller) AppendTerminalResumeHooksOnOwner(scope *cooperative.Scope, value *TerminalResume, hooks []protocol.JSONObject) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 0 {
		return cooperative.ErrRestricted
	}
	for _, hook := range hooks {
		value.messages = append(value.messages, queryloop.Message{Record: hook.Clone()})
	}
	// Interactive Source has Todo v2 enabled. It preserves its actual tasks and
	// legacy todo map while restoring nonempty file-history snapshots.
	if err := c.restoreFileHistoryAt(value.log, c.options.Bootstrap.Snapshot().OriginalCWD); err != nil {
		return err
	}
	value.stage = 1
	return nil
}

// RestoreTerminalResumeReadsOnOwner uses the same native Read/Write/Edit history
// extractor and Source mergeFileStateCaches: existing newer reads survive; a
// newer recovered entry replaces one key, and Get still updates LRU recency.
func (c *Controller) RestoreTerminalResumeReadsOnOwner(scope *cooperative.Scope, value *TerminalResume, originalCWD string) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 1 {
		return cooperative.ErrRestricted
	}
	root := value.log.String("projectPath")
	if root == "" {
		root = originalCWD
	}
	rows := []*protocol.JSONObject{}
	for _, item := range value.messages {
		row := item.Record
		rows = append(rows, &row)
	}
	recovered, err := extractReadCache(rows, root, c.options.ResumeOptions.Environment["HOME"], c.options.WriterOptions.Files, c.options.ResumeOptions.ParseDate, c.readCache.maxEntries)
	if err != nil {
		return err
	}
	merged := c.readCache.Clone()
	for _, path := range recovered.order {
		next := recovered.entries[path]
		previous, exists := merged.Get(path)
		if !exists || next.Timestamp > previous.Timestamp {
			merged.Set(path, next)
		}
	}
	c.readCache = merged
	value.recovered = recovered
	value.stage = 2
	return nil
}

// TerminalResumeRecoveredReadsOnOwner exposes only this transition's real
// extractor result. The native tools merge it into their existing complete
// read states, retaining offsets/ranges and recency of unrelated live entries.
func (c *Controller) TerminalResumeRecoveredReadsOnOwner(scope *cooperative.Scope, value *TerminalResume) (*ReadCache, error) {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return nil, err
	}
	if value == nil || value.stage != 2 || value.recovered == nil {
		return nil, cooperative.ErrRestricted
	}
	return value.recovered.Clone(), nil
}

func (c *Controller) SwitchTerminalResumeSessionOnOwner(scope *cooperative.Scope, value *TerminalResume) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 2 {
		return cooperative.ErrRestricted
	}
	var project *string
	if path := value.log.String("fullPath"); path != "" {
		dir := filepath.Dir(path)
		project = &dir
	}
	if err := c.options.Bootstrap.SwitchSession(scope, value.log.String("sessionId"), project); err != nil {
		return err
	}
	dir := ""
	if project != nil {
		dir = *project
	}
	c.writer.Switch(value.log.String("sessionId"), dir)
	value.stage = 3
	return nil
}

// ResetTerminalResumeMetadataOnOwner follows the actual recording rename await
// and resetSessionFilePointer await. Metadata is cleared before truthy restore.
func (c *Controller) ResetTerminalResumeMetadataOnOwner(scope *cooperative.Scope, value *TerminalResume) error {
	if err := c.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 3 {
		return cooperative.ErrRestricted
	}
	reset, err := c.writer.BeginResetSessionFile(scope)
	if err != nil {
		return err
	}
	if _, err = reset.Await(scope); err != nil {
		return err
	}
	c.writer.mu.Lock()
	c.writer.metadata = protocol.NewJSONObject()
	c.writer.lastPrompt = nil
	c.writer.mu.Unlock()
	c.writer.RestoreMetadata(value.log)
	c.promptID = nil
	value.stage = 4
	return nil
}

func (a *SerialASK) InstallTerminalResumeOnOwner(scope *cooperative.Scope, value *TerminalResume) error {
	if a == nil || a.running || a.pending == nil {
		return cooperative.ErrRestricted
	}
	if err := a.controller.checkTerminalResume(scope, value); err != nil {
		return err
	}
	if value == nil || value.stage != 4 {
		return cooperative.ErrRestricted
	}
	cache := a.controller.readCache
	if err := a.ReplaceCompactHistory(scope, value.messages); err != nil {
		return err
	}
	// This transition must not share compact/rewind's read-cache clear.
	a.controller.readCache = cache
	value.stage = 5
	return nil
}
