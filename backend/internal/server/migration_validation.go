package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
)

var migrationSessionName = regexp.MustCompile(`(?i)^[a-f0-9-]{36}\.jsonl$`)

// Validation deliberately does not repair JSON, install schemas, start services,
// migrate indexes, refresh credentials, or publish any startup pointer.
func (s *Server) validateMigrationData(ctx context.Context) error {
	root := s.options.ConfigDir
	stat, err := os.Stat(root)
	if err != nil || !stat.IsDir() {
		return errors.New("Data directory is unavailable")
	}
	env := map[string]string{}
	if s.options.Environment != nil {
		env = s.options.Environment()
	}
	var config *string
	if value, present := env["CLAUDE_CONFIG_DIR"]; present {
		config = &value
	}
	suffix := ""
	if env["CLAUDE_CODE_CUSTOM_OAUTH_URL"] != "" {
		suffix = "-custom-oauth"
	} else if env["USER_TYPE"] == "ant" {
		truthy := func(value string) bool { return value == "1" || value == "true" || value == "yes" || value == "on" }
		if truthy(env["USE_LOCAL_OAUTH"]) {
			suffix = "-local-oauth"
		} else if truthy(env["USE_STAGING_OAUTH"]) {
			suffix = "-staging-oauth"
		}
	}
	global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: s.options.HomeDir, ConfigDir: config, OAuthFileSuffix: suffix})
	if err != nil {
		return err
	}
	for _, path := range []string{filepath.Join(root, "settings.json"), filepath.Join(root, "cc-haha", "settings.json"), global.Path()} {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &value); err != nil || value == nil {
			return fmt.Errorf("Invalid migrated configuration: %s", path)
		}
	}
	projects := filepath.Join(root, "projects")
	entries, err := os.ReadDir(projects)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var representative string
	for _, project := range entries {
		if !project.IsDir() {
			continue
		}
		directory := filepath.Join(projects, project.Name())
		files, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		for _, entry := range files {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			// Source Dirent.isFile() excludes symbolic links and named pipes.
			path := filepath.Join(directory, entry.Name())
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			var first [1]byte
			_, readError := file.Read(first[:])
			closeError := file.Close()
			if readError != nil && !errors.Is(readError, io.EOF) {
				return readError
			}
			if closeError != nil {
				return closeError
			}
			if representative == "" && migrationSessionName.MatchString(entry.Name()) {
				representative = path
			}
		}
	}
	if representative != "" {
		request, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost/messages?limit=1&projectContext=false", nil)
		// Read and project using the same bounded native history path used by chat.
		_, err := s.messages(ctx, sessionInfo{Path: representative}, request)
		return err
	}
	return nil
}
