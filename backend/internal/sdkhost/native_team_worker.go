package sdkhost

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

type NativeTeamPreset struct {
	Tools    []string
	Messages []protocol.JSONObject
}

// NativeTeamWorker is minted on the actual main Controller's owner. An Agent
// ID or environment flag alone cannot change sidechain/lifecycle semantics.
type NativeTeamWorker struct {
	owner      *cooperative.Owner
	controller *sessioncontroller.Controller
	agentID    string
	prepare    func(context.Context, *cooperative.Scope, []string) (NativeTeamPreset, error)
	activated  bool
	tools      []string
}

func NewNativeTeamWorker(scope *cooperative.Scope, owner *cooperative.Owner, controller *sessioncontroller.Controller, prepare func(context.Context, *cooperative.Scope, []string) (NativeTeamPreset, error)) (*NativeTeamWorker, error) {
	if !scope.BelongsTo(owner) || controller == nil || prepare == nil {
		return nil, cooperative.ErrRestricted
	}
	team, name, id, err := controller.TeamWorkerIdentity(scope)
	if err != nil || team == "" || name == "" || id == "" {
		return nil, cooperative.ErrRestricted
	}
	return &NativeTeamWorker{owner: owner, controller: controller, agentID: id, prepare: prepare}, nil
}
func (t *NativeTeamWorker) check(h *Host, scope *cooperative.Scope) error {
	if t == nil || !scope.BelongsTo(t.owner) || t.owner != h.options.Owner || t.controller != h.options.Controller || h.options.Print == nil || !h.options.Print.NativeProcess || h.options.Print.AgentID != t.agentID {
		return cooperative.ErrRestricted
	}
	_, _, id, err := t.controller.TeamWorkerIdentity(scope)
	if err != nil || id != t.agentID {
		return cooperative.ErrRestricted
	}
	return nil
}
func (h *Host) prepareNativeTeamWorker(scope *cooperative.Scope, ctx context.Context) error {
	if h.options.Print == nil || h.options.Print.TeamWorker == nil {
		if h.options.OriginalEnv["CC_HAHA_TEAM_WORKER"] == "1" {
			return errors.New("Approved team worker preset is unavailable")
		}
		return nil
	}
	t := h.options.Print.TeamWorker
	if err := t.check(h, scope); err != nil {
		return err
	}
	if t.activated {
		return nil
	}
	available := []string{}
	for _, tool := range h.registered {
		available = append(available, tool.Name)
	}
	preset, err := t.prepare(ctx, scope, available)
	if err != nil {
		return err
	}
	t.tools = append([]string{}, preset.Tools...)
	if err = h.initializePrintCatalog(scope); err != nil {
		return err
	}
	for _, message := range preset.Messages {
		if err = h.serial.Accept(scope, queryloop.Message{Record: message}); err != nil {
			return err
		}
	}
	t.activated = true
	return nil
}

func (h *Host) ApplyNativeTeamModel(scope *cooperative.Scope, requested, resolved, display string) error {
	if h.options.Print == nil || h.options.Print.TeamWorker == nil {
		return cooperative.ErrRestricted
	}
	if err := h.options.Print.TeamWorker.check(h, scope); err != nil {
		return err
	}
	return h.ApplyNativeSessionModel(scope, requested, resolved, display)
}
