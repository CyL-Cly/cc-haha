package securestorage

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func legacyServiceName(oauth string) string { return strings.Replace(oauth, "-credentials", "", 1) }

// ReadLegacyManagedAPIKey uses the original /login API-key identity, distinct
// from the OAuth credentials suffix, through this Store's same native primitive.
// Caching and config fallback belong to the original CLI memoized getter.
func (s *Store) ReadLegacyManagedAPIKey(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.options.Platform != "darwin" {
		return "", nil
	}
	service := legacyServiceName(s.keychainServiceName())
	args := []string{"find-generic-password", "-a"}
	if name := s.keychainRawUser(); name != "" {
		args = append(args, name)
	}
	args = append(args, "-w", "-s", service)
	data, err := s.options.RunSecurity(ctx, Command{Args: args, ShellScript: `security find-generic-password -a $USER -w -s "` + service + `"`})
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	if err != nil {
		// Original execSyncWithDefaults uses reject:false and returns stdout
		// even for a nonzero process receipt. Spawn failures become null;
		// a thrown replacement primitive remains observable to its caller.
		if !nativeProcessReceipt(err) {
			return "", err
		}
	}
	return protocol.TrimJavaScriptString(string(protocol.DecodeJavaScriptUTF8(data))), ctx.Err()
}

// The legacy /login key is a different identity from the OAuth record. Source
// logout attempts its deletion, then still clears the config fallback.
func (s *Store) RemoveLegacyManagedAPIKey(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.options.Platform != "darwin" {
		return nil
	}
	args := []string{"delete-generic-password", "-a"}
	if name := s.keychainRawUser(); name != "" {
		args = append(args, name)
	}
	service := strings.Replace(s.keychainServiceName(), "-credentials", "", 1)
	args = append(args, "-s", service)
	if _, err := s.options.RunSecurity(ctx, Command{Args: args, ShellScript: `security delete-generic-password -a $USER -s "` + service + `"`}); err != nil {
		return errors.New("Failed to delete keychain entry")
	}
	return nil
}
