package sdkhost

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypreflight"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type ContextInspectionInputs struct {
	Model, MainModel string
	History          []protocol.JSONObject
	Definitions      []toolschema.Definition
	Schemas          []protocol.JSONObject
}

// SnapshotContextInspection reads this same host's admitted executable pool,
// mutable messages and runtime model. It never creates a new SDK host/ASK.
func (h *Host) SnapshotContextInspection(scope *cooperative.Scope) (ContextInspectionInputs, error) {
	if err := h.checkInferenceOwner(scope); err != nil {
		return ContextInspectionInputs{}, err
	}
	mainModel := h.options.Model
	_, permissions, err := h.options.Controller.SDKSettingsOnOwner(scope)
	if err != nil {
		return ContextInspectionInputs{}, err
	}
	model := mainModel
	if h.options.Print.ResolveRuntimeModel != nil {
		model, err = h.options.Print.ResolveRuntimeModel(scope, querypreflight.RuntimeModelInput{MainLoopModel: mainModel, PermissionMode: permissions.String("mode")})
		if err != nil {
			return ContextInspectionInputs{}, err
		}
	}
	records, err := querypreflight.InspectionRecords(h.serial.History())
	if err != nil {
		return ContextInspectionInputs{}, err
	}
	definitions := append([]toolschema.Definition{}, h.definitions...)
	host := &toolschema.Host{MainLoopModel: model, BaseURL: h.options.OriginalEnv["ANTHROPIC_BASE_URL"]}
	if h.options.Print.Tools != nil {
		host, err = h.options.Print.Tools.SchemaHostForScope(scope, model, h.options.OriginalEnv["ANTHROPIC_BASE_URL"])
		if err != nil {
			return ContextInspectionInputs{}, err
		}
	}
	schemas, err := h.toolSchemas.RenderBatch(definitions, toolschema.Options{Host: *host, Model: model})
	if err != nil {
		return ContextInspectionInputs{}, err
	}
	if err := scope.AwaitFulfilled(); err != nil {
		return ContextInspectionInputs{}, err
	}
	return ContextInspectionInputs{Model: model, MainModel: mainModel, History: records, Definitions: definitions, Schemas: schemas}, nil
}
