package clibootstrap

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

var noContinueHistory = errors.New("No conversation found to continue")

func (m *MainPrint) activateNativeContinue(scope *cooperative.Scope) error {
	// Source --continue takes precedence over --resume and intentionally calls
	// loadConversationForResume without a resumeSessionAt boundary.
	future, err := m.Controller.BeginActivate(scope, sessioncontroller.Activation{Resume: resumestate.Input{}, ForkSession: m.options.Arguments.ForkSession, ValidateLoaded: func(result *resumestate.Result) error {
		if result == nil {
			return noContinueHistory
		}
		return nil
	}})
	if err != nil {
		return err
	}
	_, err = future.Await(scope)
	if errors.Is(err, noContinueHistory) && !m.options.Interactive {
		// Unlike the TUI's explicit no-history error, headless Source falls
		// through to normal fresh-session startup when there is no prior log.
		fresh, failure := m.Controller.BeginActivateFreshOwned(scope, sessioncontroller.FreshPrintProfile)
		if failure != nil {
			return failure
		}
		_, failure = fresh.Await(scope)
		return failure
	}
	return err
}
