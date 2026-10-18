package ccrsessions

import (
	"context"
	"errors"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Teleported struct {
	Session     protocol.JSONObject
	Messages    []protocol.JSONObject
	Branch      string
	BranchError error
}

func (s *Service) Teleport(ctx context.Context, sid string, interactive bool) (Teleported, error) {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return Teleported{}, err
	}
	defer done()
	allowed, err := s.PolicyRemoteAllowed(ctx)
	if err != nil {
		return Teleported{}, err
	}
	if !allowed {
		return Teleported{}, errors.New("Remote sessions are disabled by your organization's policy.")
	}
	if sid == "" {
		return Teleported{}, errors.New("No session ID provided for teleport")
	}
	// Source print validates clean Git first. The interactive direct path
	// resolves repository mismatch before prompting about tracked changes.
	if !interactive {
		if err := s.ValidateGitState(ctx); err != nil {
			return Teleported{}, err
		}
	}
	session, err := s.FetchSession(ctx, sid)
	if err != nil {
		return Teleported{}, err
	}
	if err := s.ValidateRepository(ctx, session).Error(sid); err != nil {
		return Teleported{}, err
	}
	if interactive {
		if err := s.ValidateGitState(ctx); err != nil {
			return Teleported{}, err
		}
	}
	rows, err := s.FetchTranscript(ctx, sid)
	if err != nil {
		return Teleported{}, err
	}
	branch := Branch(session)
	branchError := s.CheckoutBranch(ctx, branch)
	if ctx.Err() != nil {
		return Teleported{}, context.Cause(ctx)
	}
	return Teleported{Session: session, Messages: rows, Branch: branch, BranchError: branchError}, nil
}
