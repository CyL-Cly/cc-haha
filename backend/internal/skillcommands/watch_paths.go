package skillcommands

import (
	"context"
	"path/filepath"
)

// WatchPaths mirrors skillChangeDetector.getWatchablePaths, rather than the
// recursive discovery roots. The watcher itself admits only existing paths.
func (s *Service) WatchPaths(ctx context.Context, computerUseConfig string) []string {
	directory := s.configDirectory()
	paths := []string{filepath.Join(directory, "skills"), filepath.Join(directory, "commands"), computerUseConfig, filepath.Join(s.options.CWD, ".claude", "skills"), filepath.Join(s.options.CWD, ".claude", "commands")}
	directories := s.options.AdditionalDirectories(ctx)
	for _, dir := range directories {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(s.options.CWD, dir)
		}
		paths = append(paths, filepath.Join(dir, ".claude", "skills"))
	}
	userRoots := s.options.Roots.UserRoots(ctx)
	for _, root := range userRoots {
		if root.Flavor == "agents" {
			paths = append(paths, root.Path, filepath.Join(s.options.CWD, ".agents", "skills"))
			for _, dir := range directories {
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(s.options.CWD, dir)
				}
				paths = append(paths, filepath.Join(dir, ".agents", "skills"))
			}
			break
		}
	}
	return paths
}
