package application

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillcommands"
	"github.com/NanmiCoder/cc-haha/backend/internal/skillregistry"
	"github.com/NanmiCoder/cc-haha/backend/internal/slashcommandsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

func newDesktopSlashCommands(native *clibootstrap.NativeBoundary, settings *clibootstrap.NativeProcessSettings, skills *skillregistry.Service, stderr io.Writer) (*slashcommandsapi.Service, error) {
	commands, err := settings.SkillCommands(clibootstrap.NativeSkillOptions{})
	if err != nil {
		return nil, err
	}
	comparer := collate.New(language.English)
	var compareMu sync.Mutex
	return slashcommandsapi.New(slashcommandsapi.Options{
		Skills: skills,
		LocalCommands: func(ctx context.Context, cwd string) ([]protocol.JSONObject, error) {
			definitions, err := commands.Local(ctx, cwd)
			if err != nil {
				return nil, err
			}
			out := make([]protocol.JSONObject, 0, len(definitions))
			for _, definition := range definitions {
				out = append(out, definition.Metadata())
			}
			return out, nil
		},
		CompiledCommands: func(ctx context.Context) ([]protocol.JSONObject, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			bundled, err := skillcommands.NativeExternalBundled(skillcommands.BundledOptions{Environment: native.Environment, IsGit: func(ctx context.Context) (bool, error) {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				return usercontext.GitRoot(usercontext.OSMemoryFiles{}, native.CWD) != "", nil
			}})
			if err != nil {
				return nil, err
			}
			return sdkhost.CompiledHeadlessCommands(native.Environment, bundled), nil
		},
		Compare: func(a, b string) int { compareMu.Lock(); defer compareMu.Unlock(); return comparer.CompareString(a, b) },
		Warn: func(err error) {
			_, _ = fmt.Fprintf(stderr, "[skills] built-in slash commands unavailable, listing disk skills only: %v\n", err)
		},
	})
}
