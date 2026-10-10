package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// SyntheticToolBinding is supplied only by the actual worker's compiled-schema
// handler. It retains the existing canonical registry and rejects foreign owners.
type SyntheticToolBinding interface {
	Owns(*cooperative.Owner) bool
	SyntheticToolNames(*cooperative.Scope) ([]string, error)
}

type PublicJSONSchemaBinding interface {
	SyntheticToolBinding
	OwnsPublicJSONSchema(*cooperative.Owner) bool
}

// Only the parent's actual compiled CLI schema may add this transient tool.
// Worker/foreign bindings cannot widen the parent's canonical catalog.
func (c *Controller) InstallNativePublicJSONSchemaTools(scope *cooperative.Scope, tools []protocol.JSONObject, mcp *mcptools.Service, schema PublicJSONSchemaBinding) error {
	if c == nil || !c.options.PublicPrint || !c.options.NativeTools || c.options.Bootstrap == nil || c.options.WriterOptions.AgentID != "" || c.options.QuerySource == "workflow_agent" || schema == nil || !schema.OwnsPublicJSONSchema(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	return c.installNativeSyntheticTools(scope, tools, mcp, schema)
}

func (c *Controller) InstallNativeSyntheticPrintTools(scope *cooperative.Scope, tools []protocol.JSONObject, mcp *mcptools.Service, synthetic SyntheticToolBinding) error {
	if c == nil || !c.options.NativeTools || c.options.Bootstrap == nil || synthetic == nil || !synthetic.Owns(c.options.Bootstrap.Owner()) || c.options.QuerySource != "workflow_agent" || c.options.WriterOptions.AgentID == "" {
		return cooperative.ErrRestricted
	}
	return c.installNativeSyntheticTools(scope, tools, mcp, synthetic)
}

func (c *Controller) installNativeSyntheticTools(scope *cooperative.Scope, tools []protocol.JSONObject, mcp *mcptools.Service, synthetic SyntheticToolBinding) error {
	names := map[string]bool{}
	if mcp != nil {
		definitions, _, e := mcp.Catalog(scope)
		if e != nil {
			return e
		}
		for _, definition := range definitions {
			names[definition.Name] = true
		}
	}
	registered, e := synthetic.SyntheticToolNames(scope)
	if e != nil {
		return e
	}
	if len(registered) != 1 || registered[0] != "StructuredOutput" {
		return cooperative.ErrRestricted
	}
	names[registered[0]] = true
	return c.installPrintTools(scope, tools, names)
}
