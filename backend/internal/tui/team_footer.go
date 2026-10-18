package tui

import "context"

type TerminalTeamFooterSource interface {
	SubscribeTerminalTeamFooter(context.Context, func(bool)) (bool, func(), error)
}

type terminalPaneTeamChangedMsg bool
