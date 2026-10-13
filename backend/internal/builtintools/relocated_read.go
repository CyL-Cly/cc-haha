package builtintools

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storagerelocations"
	"path/filepath"
)

func (s *Service) readPath(in protocol.JSONObject) (string, error) {
	path, err := s.path(in, "file_path")
	if err != nil {
		return "", err
	}
	config := s.options.Environment["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(s.options.Home, ".claude")
	}
	return storagerelocations.Resolve(path, config)
}
