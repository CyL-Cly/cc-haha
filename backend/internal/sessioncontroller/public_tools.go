package sessioncontroller

import (
	"bytes"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcptools"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) InstallPrintTools(scope *cooperative.Scope, tools []protocol.JSONObject) error {
	return c.installPrintTools(scope, tools, nil)
}
func (c *Controller) InstallNativeMCPPrintTools(scope *cooperative.Scope, tools []protocol.JSONObject, mcp *mcptools.Service) error {
	if mcp == nil {
		return cooperative.ErrRestricted
	}
	definitions, _, err := mcp.Catalog(scope)
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, d := range definitions {
		names[d.Name] = true
	}
	return c.installPrintTools(scope, tools, names)
}
func (c *Controller) installPrintTools(scope *cooperative.Scope, tools []protocol.JSONObject, mcpNames map[string]bool) error {
	if c == nil || !c.options.PublicPrint || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		kind := tool.String("kind")
		if len(tool.OwnProperties()) != 2 || kind != "builtin" && !(c.options.NativeTools && kind == "registered" && mcpNames[tool.String("name")]) || tool.String("name") == "" || (!c.options.NativeTools && (tool.String("name") != "Write" || c.options.BaseToolsCLI[0] != "Write")) {
			return capability("public executable tool catalog binding")
		}
		if seen[tool.String("name")] {
			return capability("duplicate public native tool contribution")
		}
		seen[tool.String("name")] = true
	}
	if !c.options.NativeTools && len(tools) > 1 {
		return capability("duplicate public Write contribution")
	}
	c.registeredTools = make([]protocol.JSONObject, len(tools))
	for i, t := range tools {
		c.registeredTools[i] = t.Clone()
	}
	return nil
}
func (c *Controller) checkTools(tools []protocol.JSONObject) error {
	if !c.options.PublicPrint {
		if len(tools) > 0 {
			return capability("nonempty host tool definitions")
		}
		return nil
	}
	if len(tools) != len(c.registeredTools) {
		return capability("tool catalog differs from installed native registry")
	}
	for i, t := range tools {
		a, _ := t.MarshalJSON()
		b, _ := c.registeredTools[i].MarshalJSON()
		if !bytes.Equal(a, b) {
			return capability("foreign public native tool definition")
		}
	}
	return nil
}

// BorrowNativeCopyBinding exposes the Controller's already-created native
// resource ledger to genuine tool services. It does not consume the unique SDK
// Host borrower, allocate a second Writer or change the duplicate-Host guard.
func (c *Controller) BorrowNativeCopyBinding(owner *cooperative.Owner) (*ownerasync.NativeBinding, error) {
	if c == nil || c.options.Bootstrap == nil || !c.options.Bootstrap.Owns(owner) || c.writer == nil || c.writer.async == nil {
		return nil, cooperative.ErrRestricted
	}
	binding := c.writer.async.NativeCopyBinding()
	if binding == nil || !binding.Owns(owner) {
		return nil, cooperative.ErrRestricted
	}
	return binding, nil
}
