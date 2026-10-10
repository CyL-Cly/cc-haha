package tui

import "context"

// The Session owns persistence eligibility and the actual title/SID. Terminal
// presentation only decides whether its output is a TTY, as Source does.
type ResumeHintSession interface {
	ResumeHint(context.Context) (string, error)
}
