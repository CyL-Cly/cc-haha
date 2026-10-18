package tui

import "strings"

type ConversationExportPresentation struct {
	Version, WorkingDirectory, Model, BillingType, Welcome, Agent string
	Columns                                                       int
	FullLogo, Onboarding, HomeDirectory, Opus1mNotice             bool
	Organization, Terminal                                        string
	Tips, ReleaseNotes                                            []string
	Status                                                        ExportStatusContext
	OpusNoticeSeenCount                                           float64
	Announcement, DebugDestination, TmuxSession, TmuxPrefix       string
	DebugMode, TmuxPrefixConflicts                                bool
	GuestPasses, OverageCreditUpsell, ResetPasses, Sandboxed      bool
	GuestPassSubtitle, OverageCreditAmount                        string
	PassesRemaining                                               float64
	GuestPassReward                                               bool
	Activities                                                    []ExportActivity
	NowMillis                                                     float64
}

type ExportActivity struct {
	Description    string
	ModifiedMillis float64
}

// Export retains the original visible session identification and accounting
// labels from the live host. Terminal framing uses the native Go presentation.
func RenderConversationExportHeader(value ConversationExportPresentation) string {
	rows := renderExportLogo(value)
	if value.Opus1mNotice {
		rows = append(rows, "")
		for i, line := range exportWrapWords(" Opus now defaults to 1M context · 5x more room, same pricing", max(1, value.Columns-3)) {
			prefix := "   "
			if i == 0 {
				prefix = "  ↑"
			}
			rows = append(rows, strings.TrimRight(prefix+line, " "))
		}
	}
	// Original compact Logo has no business notices; full-width and condensed
	// modes mount these independent rows after the market notice.
	if !value.FullLogo || value.Columns >= 70 {
		business := []string{}
		if value.DebugMode {
			business = append(business, "Debug mode enabled", "Logging to: "+value.DebugDestination)
		}
		if value.TmuxSession != "" {
			business = append(business, "tmux session: "+value.TmuxSession)
			if value.TmuxPrefixConflicts {
				business = append(business, "Detach: "+value.TmuxPrefix+" "+value.TmuxPrefix+" d (press prefix twice - Claude uses "+value.TmuxPrefix+")")
			} else {
				business = append(business, "Detach: "+value.TmuxPrefix+" d")
			}
		}
		if value.Announcement != "" {
			if value.Organization != "" {
				business = append(business, "Message from "+value.Organization+":")
			}
			business = append(business, value.Announcement)
		}
		if value.FullLogo && value.Sandboxed {
			business = append(business, "Your bash commands will be sandboxed. Disable with /sandbox.")
		}
		if len(business) > 0 {
			rows = append(rows, "")
			for _, line := range business {
				for _, wrapped := range exportWrapWords(line, max(1, value.Columns-2)) {
					rows = append(rows, strings.TrimRight("  "+wrapped, " "))
				}
			}
		}
	}
	if value.FullLogo && value.Columns < 70 && value.Sandboxed {
		// Compact Logo's own row has marginTop=1 after the sibling notice
		// allocation. Ink trims the spaces at every rendered line boundary.
		rows = append(rows, "", "")
		for _, line := range exportWrapWords("Your bash commands will be sandboxed. Disable with /sandbox.", max(1, value.Columns)) {
			rows = append(rows, strings.TrimRight(line, " "))
		}
	}
	if notices := ExportStatusNoticeLines(value.Status, max(1, value.Columns)); len(notices) > 0 {
		rows = append(rows, "", strings.Join(notices, "\n"))
	}
	return strings.Join(rows, "\n")
}
