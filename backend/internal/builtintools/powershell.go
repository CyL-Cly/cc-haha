package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func (s *Service) validatePowerShell(scope *cooperative.Scope) error {
	if err := s.check(scope); err != nil {
		return err
	}
	if windowshell.IsWindows(s.options.Platform) {
		return s.checkWindowsPowerShellSandbox(scope)
	}
	return nil
}

func (s *Service) callPowerShell(ctx context.Context, input protocol.JSONObject, call queryloop.JSONToolCallContext) (queryloop.ToolCallResult, error) {
	scope := scopeFor(call.Opaque)
	if err := s.validatePowerShell(scope); err != nil {
		return queryloop.ToolCallResult{}, err
	}
	if err := context.Cause(ctx); err != nil {
		return queryloop.ToolCallResult{}, err
	}
	view, err := s.bashContextView(scope, call.Opaque)
	if err != nil {
		return queryloop.ToolCallResult{}, err
	}
	view, err = view.powerShellView(scope)
	if err != nil {
		return queryloop.ToolCallResult{}, err
	}
	path, err := view.PowerShellPath(scope)
	if err != nil {
		return queryloop.ToolCallResult{}, err
	}
	if path == "" {
		return queryloop.ToolCallResult{Data: object(field("stdout", ""), field("stderr", "PowerShell is not available on this system."), field("interrupted", false), field("isImage", false))}, nil
	}
	result, err := view.callBashOnContext(ctx, input, call)
	if err != nil {
		var executable *exec.Error
		var physical *os.PathError
		if errors.As(err, &executable) || errors.As(err, &physical) {
			return queryloop.ToolCallResult{Data: object(field("stdout", ""), field("stderr", "Failed to execute PowerShell command: "+err.Error()), field("interrupted", false), field("isImage", false))}, nil
		}
		return queryloop.ToolCallResult{}, err
	}
	data := inputObject(result.Data)
	if data.String("backgroundTaskId") == "" {
		data, err = s.normalizeShellImage(ctx, scope, data)
		if err != nil {
			return queryloop.ToolCallResult{}, err
		}
		result.Data = raw(data)
	}
	return result, nil
}

func (s *Service) powerShell(ctx context.Context, input protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	result, err := s.callPowerShell(ctx, input, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
	return result.Data, err
}

func (s *Service) mapPowerShellResult(data, id json.RawMessage) (protocol.JSONObject, error) {
	result := inputObject(data)
	if boolean(result, "isImage", false) {
		if image, parsed := shellImageResult(result, id); parsed {
			return image, nil
		}
	}
	return s.mapBashResult(data, id)
}
