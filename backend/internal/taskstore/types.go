// Package taskstore ports the Source task-list filesystem transactions. It has
// no model loop, tool registry, AppState, or session owner of its own.
package taskstore

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Options struct {
	ConfigDir string
	UserType  string
	PID       int
	Now       func() time.Time
	// Source tasksUpdated fires after each physical write. The callback owns
	// no session state; hosts may deliver it to their actual event owner.
	OnUpdate func()
}

type Service struct {
	options         Options
	mu              sync.Mutex
	precision       string
	teamTransaction *heldTeamLifecycle
}

func New(options Options) (*Service, error) {
	if !filepath.IsAbs(options.ConfigDir) || options.Now == nil || options.PID < 1 {
		return nil, errors.New("native task store requires original config path, clock and process ID")
	}
	return &Service{options: options}, nil
}

type Creation struct {
	TaskID      string
	CommittedAt string
	Revision    float64
}

type Mutation struct {
	Previous, Task protocol.JSONObject
	Updated        bool
	CommittedAt    string
	Revision       float64
}

type Deletion struct {
	Deleted     bool
	CommittedAt string
	Revision    *float64
}

type Snapshot struct {
	Tasks      []protocol.JSONObject
	CapturedAt string
	Revision   float64
}

type lifecycle struct {
	Generation float64
	Deleted    bool
	Identity   json.RawMessage
	Terminals  []protocol.JSONObject
}
