package attachments

import _ "embed"

// Literal original getAutoModeFullInstructions content. Factory/wrapping remains
// the same attachment projection, with the original UUID→Date allocation order.
//
//go:embed assets/auto-mode-full.md
var autoModeFull string

const autoModeSparse = "Auto mode still active (see full instructions earlier in conversation). Execute autonomously, minimize interruptions, prefer action over planning."
