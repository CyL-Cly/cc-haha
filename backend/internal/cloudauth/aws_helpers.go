package cloudauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type CommandOutput struct {
	Stdout, Stderr string
	ExitCode       int
}
type CloudCommand func(context.Context, string, time.Duration) (CommandOutput, error)

type AWSHelperOptions struct {
	Refresh, Export                       string
	RefreshFromProject, ExportFromProject bool
	Trusted, NonInteractive               bool
	// Probe is an actual STS caller-identity consumer bound to the same frozen
	// credential resolver. Only its transport is an external primitive seam.
	Probe    func(context.Context) error
	Run      CloudCommand
	ClearINI func(context.Context) error
	Error    func(string)
}

// RefreshAndExport is auth.ts's combined producer, before CloudCache applies
// the original one-hour memoization. A valid existing identity skips each
// command independently; refresh success still precedes the export probe.
func RefreshAndExport(ctx context.Context, options AWSHelperOptions) (*AWSCredentials, error) {
	refreshed := false
	if options.Refresh != "" && (!options.RefreshFromProject || options.Trusted || options.NonInteractive) {
		if options.Probe == nil || options.Run == nil {
			return nil, errors.New("AWS refresh requires actual STS and command primitives")
		}
		if err := options.Probe(ctx); err != nil {
			output, failure := options.Run(ctx, options.Refresh, 180*time.Second)
			refreshed = failure == nil && output.ExitCode == 0
			if !refreshed && options.Error != nil {
				options.Error("Error running awsAuthRefresh (in settings or ~/.claude.json):")
			}
		}
	}
	var credentials *AWSCredentials
	if options.Export != "" && (!options.ExportFromProject || options.Trusted || options.NonInteractive) {
		if options.Probe == nil || options.Run == nil {
			return nil, errors.New("AWS credential export requires actual STS and command primitives")
		}
		if err := options.Probe(ctx); err != nil {
			output, failure := options.Run(ctx, options.Export, 0)
			if failure == nil && output.ExitCode == 0 && output.Stdout != "" {
				credentials = ParseCredentialExport([]byte(strings.TrimFunc(output.Stdout, jsSpace)))
			}
			if credentials == nil && options.Error != nil {
				options.Error("Error getting AWS credentials from awsCredentialExport (in settings or ~/.claude.json):")
			}
		}
	}
	if refreshed || credentials != nil {
		if options.ClearINI == nil {
			return nil, errors.New("AWS helper requires actual INI cache refresh consumer")
		}
		// Source deliberately ignores cache-clear errors, after an actual attempt.
		_ = options.ClearINI(ctx)
	}
	return credentials, nil
}

func ParseCredentialExport(raw []byte) *AWSCredentials {
	var value struct {
		Credentials struct {
			AccessKeyID                   *string `json:"AccessKeyId"`
			SecretAccessKey, SessionToken *string
		}
	}
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	credentials := value.Credentials
	if credentials.AccessKeyID == nil || *credentials.AccessKeyID == "" || credentials.SecretAccessKey == nil || *credentials.SecretAccessKey == "" || credentials.SessionToken == nil || *credentials.SessionToken == "" {
		return nil
	}
	return &AWSCredentials{AccessKeyID: *credentials.AccessKeyID, SecretAccessKey: *credentials.SecretAccessKey, SessionToken: credentials.SessionToken}
}
