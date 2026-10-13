package storagemetadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Request struct {
	SourceDir string `json:"sourceDir"`
	TargetDir string `json:"targetDir"`
	StagedDir string `json:"stagedDir"`
}

// RunWorker enters before Agent/bootstrap. Its only filesystem mutation is the
// explicitly supplied private staging copy inside the migration target.
func RunWorker(ctx context.Context, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(input, 64*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 64*1024 {
		return errors.New("Storage relocation worker request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("Storage relocation worker accepts exactly one request")
		}
		return err
	}
	for _, directory := range []string{request.SourceDir, request.TargetDir, request.StagedDir} {
		if !filepath.IsAbs(directory) || strings.ContainsRune(directory, 0) {
			return errors.New("Storage relocation worker requires explicit absolute directories")
		}
	}
	target, stage := filepath.Clean(request.TargetDir), filepath.Clean(request.StagedDir)
	if !inside(target, stage) || filepath.Dir(stage) != target || !strings.HasPrefix(filepath.Base(stage), ".cc-haha-migration-") || filepath.Base(stage) == ".cc-haha-migration-" {
		return errors.New("Storage relocation worker requires a private staged target")
	}
	if inside(request.SourceDir, target) || inside(target, request.SourceDir) {
		return errors.New("Migration directories overlap")
	}
	info, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Storage relocation staging directory is unavailable")
	}
	if err = RelocateManagedData(ctx, request.SourceDir, request.TargetDir, request.StagedDir); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(map[string]bool{"relocated": true})
}
