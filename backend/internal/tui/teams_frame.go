package tui

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

// TerminalTeamsFrame carries the immutable native projection which supplied
// the accepted UI frame. The application verifies its concrete native issuer;
// this interface is neither a persisted value nor input from the model.
type TerminalTeamsFrame interface{ Projection() protocol.JSONObject }
