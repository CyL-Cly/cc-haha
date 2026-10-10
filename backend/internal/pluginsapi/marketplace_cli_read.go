package pluginsapi

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// CLI refresh exposes parseFileWithSchema's contextual errors, while the
// cache lookup primitive retains its existing API error representation.
func readMarketplaceCLI(location string) (protocol.JSONObject, error) {
	path := filepath.Join(location, ".claude-plugin", "marketplace.json")
	data, err := lspRead(path)
	if os.IsNotExist(err) || err != nil && strings.Contains(err.Error(), syscall.ENOTDIR.Error()) {
		path = location
		data, err = lspRead(path)
	}
	if err != nil {
		if _, ok := err.(*os.PathError); ok {
			return protocol.JSONObject{}, fmt.Errorf("%s", lspIOMessage(err))
		}
		return protocol.JSONObject{}, fmt.Errorf("Invalid JSON in %s: %s", path, err)
	}
	schemas, err := pluginschema.Declarations()
	if err != nil {
		return protocol.JSONObject{}, err
	}
	value, issues := pluginschema.Validate(schemas.Marketplace, data)
	if len(issues) > 0 {
		return protocol.JSONObject{}, fmt.Errorf("Invalid schema: %s %s", path, marketplaceIssues(issues))
	}
	return obj(value), nil
}
