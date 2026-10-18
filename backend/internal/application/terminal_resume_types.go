package application

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"sync"
)

type terminalResumePanelState struct {
	catalog           *clibootstrap.TerminalResumeCatalog
	preview           *clibootstrap.TerminalResumeSelection
	previewIndex      int
	all               bool
	completionDisplay string
	completion        chan struct{}
	completionOnce    sync.Once
	completionError   error
}
