package securestorage

import (
	"context"
	"errors"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

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
	service := strings.Replace(s.service, "-credentials", "", 1)
	args := []string{"find-generic-password", "-a"}
	if s.options.Environment["USER"] != "" {
		args = append(args, s.options.Environment["USER"])
	}
	args = append(args, "-w", "-s", service)
	data, err := s.options.RunSecurity(ctx, Command{Args: args})
	if err != nil {
		return "", err
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
	if s.options.Environment["USER"] != "" {
		args = append(args, s.options.Environment["USER"])
	}
	args = append(args, "-s", strings.Replace(s.service, "-credentials", "", 1))
	if _, err := s.options.RunSecurity(ctx, Command{Args: args}); err != nil {
		return errors.New("Failed to delete keychain entry")
	}
	return nil
}
