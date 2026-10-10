package sessioncontroller

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// ProcessedInput is the result of actual processUserInput, before Record/model
// preparation. Nil LocalResult preserves ordinary input processing.
type ProcessedInput struct {
	Messages           []protocol.JSONObject
	LocalResult        *LocalInputResult
	Model              string
	Effort             json.RawMessage
	CommandRules       []json.RawMessage
	NoQuery            bool
	StartMillis        float64
	AttachmentsHandled bool
}
type LocalInputResult struct {
	Text        string
	StartMillis float64
}
type InputProcessor struct {
	NativeMedia         bool
	ProcessQueuedBatch  func(*cooperative.Scope, []QueuedInput) (*ProcessedInput, error)
	ProcessTerminalBash func(*cooperative.Scope, json.RawMessage) (*ProcessedInput, error)
	Validate            func(json.RawMessage) error
	Process             func(*cooperative.Scope, json.RawMessage, json.RawMessage) (*ProcessedInput, error)
	ProcessQueued       func(*cooperative.Scope, json.RawMessage, json.RawMessage, bool, bool, json.RawMessage) (*ProcessedInput, error)
}

func (a *SerialASK) InstallPrintInputProcessor(scope *cooperative.Scope, p InputProcessor) error {
	if !scope.BelongsTo(a.owner) || a.running || !a.controller.options.PublicPrint || a.controller.inputProcessor != nil || p.Validate == nil || p.Process == nil {
		return capability("actual public print input processor binding")
	}
	a.controller.inputProcessor = &p
	return nil
}
func (c *Controller) validateSDKInput(input protocol.JSONObject) (content, uuid json.RawMessage, priority string, err error) {
	content, uuid, priority, err = validateSDKInput(input, c.inputProcessor != nil, c.options.NativeTools, c.inputProcessor != nil && c.inputProcessor.NativeMedia)
	if err == nil && c.inputProcessor != nil {
		err = c.inputProcessor.Validate(content)
	}
	return
}
func (a *SerialASK) LocalInputResult() *LocalInputResult { return a.controller.localInputResult }
func (a *SerialASK) PromptInputResult() *ProcessedInput  { return a.controller.promptInputResult }
func (a *SerialASK) HistoryLength() int                  { return len(a.controller.messages) }

func (c *Controller) InvokeSkill(scope *cooperative.Scope, name, path, content, agentID string) error {
	if c == nil || c.resume == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	c.resume.InvokeSkill(name, path, content, agentID)
	return nil
}

// InvokeSkillJSON has the same owner gate as the existing scalar producer.
func (c *Controller) InvokeSkillJSON(scope *cooperative.Scope, name, path json.RawMessage, content, agentID string) error {
	if c == nil || c.resume == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	return c.resume.InvokeSkillJSON(name, path, content, agentID)
}
