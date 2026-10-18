package clibootstrap

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (m *MainPrint) KnownCLIRepositoryPaths(ctx context.Context, repo string) ([]string, error) {
	var paths []string
	err := m.Owner.Run(ctx, func(*cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil || global == nil {
			return err
		}
		mapping, _ := global.Object("githubRepoPaths")
		_ = json.Unmarshal(mapping.Get(strings.ToLower(repo)), &paths)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var existing []string
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			continue
		}
		if info, err := m.options.Native.stat(path); err == nil && info.IsDir() {
			existing = append(existing, path)
		}
	}
	return existing, nil
}

func CLIWorkingDirectory(n *NativeBoundary, cwd string) *NativeBoundary {
	return n.agentScopedBoundary(cwd)
}

func (m *MainPrint) ForgetKnownCLIRepositoryPath(ctx context.Context, repo, path string) error {
	return m.Owner.Run(ctx, func(*cooperative.Scope) error {
		return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			if current == nil {
				return current, nil
			}
			mapping, _ := current.Object("githubRepoPaths")
			key := strings.ToLower(repo)
			var paths []string
			_ = json.Unmarshal(mapping.Get(key), &paths)
			updated := []string{}
			for _, candidate := range paths {
				if candidate != path {
					updated = append(updated, candidate)
				}
			}
			if len(paths) == len(updated) {
				return current, nil
			}
			nextMapping := mapping.Clone()
			if len(updated) == 0 {
				nextMapping.Delete(key)
			} else {
				nextMapping.Set(key, rawMain(updated))
			}
			next := current.Clone()
			next.Set("githubRepoPaths", rawMain(nextMapping))
			return &next, nil
		})
	})
}
