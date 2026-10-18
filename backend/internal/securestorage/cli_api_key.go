package securestorage

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// SaveLegacyManagedAPIKey is the original CLI /login slot, separate from the
// OAuth credential record. The secret travels only through security's stdin.
func (s *Store) SaveLegacyManagedAPIKey(ctx context.Context, key string) (bool, error) {
	if e := ctx.Err(); e != nil {
		return false, e
	}
	_ = s.RemoveLegacyManagedAPIKey(ctx)
	if s.options.Platform != "darwin" {
		return false, nil
	}
	service := strings.Replace(s.keychainServiceName(), "-credentials", "", 1)
	input := `add-generic-password -U -a "` + s.keychainUsername() + `" -s "` + service + `" -X "` + hex.EncodeToString([]byte(key)) + "\"\n"
	_, e := s.options.RunSecurity(ctx, Command{Args: []string{"-i"}, Input: input})
	// Original execa({reject:false}) resolves process failures, including
	// a nonzero exit and spawn ENOENT. Only a thrown primitive uses fallback.
	if ctx.Err() != nil {
		return false, context.Cause(ctx)
	}
	var exit *exec.ExitError
	var path *os.PathError
	var lookup *exec.Error
	if e != nil && !errors.As(e, &exit) && !errors.As(e, &path) && !errors.As(e, &lookup) {
		return false, nil
	}
	return true, nil
}
