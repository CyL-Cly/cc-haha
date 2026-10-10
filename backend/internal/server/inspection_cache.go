package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type inspectionPending struct {
	done  chan struct{}
	value inspectionTranscriptSnapshot
	err   error
}

func copyInspectionSnapshot(value inspectionTranscriptSnapshot) inspectionTranscriptSnapshot {
	value.LaunchInfo = value.LaunchInfo.Clone()
	value.Metadata = value.Metadata.Clone()
	value.Usage = append(json.RawMessage(nil), value.Usage...)
	value.ContextEstimate = append(json.RawMessage(nil), value.ContextEstimate...)
	return value
}
func (s *Server) inspectionContextRevision(ctx context.Context) string {
	_, index := s.inspectionProviders(ctx)
	value := protocol.NewJSONObject()
	if index != nil {
		value.Set("activeId", index["activeId"])
		var providers []storage.Object
		_ = json.Unmarshal(index["providers"], &providers)
		projected := []protocol.JSONObject{}
		for _, provider := range providers {
			row := protocol.NewJSONObject()
			for _, key := range []string{"id", "models", "modelContextWindows", "model1mSupport", "autoCompactWindow"} {
				if provider[key] != nil {
					row.Set(key, provider[key])
				}
			}
			projected = append(projected, row)
		}
		value.Set("providers", recoveryJSON(projected))
	}
	env := map[string]string{}
	if s.options.Environment != nil {
		env = s.options.Environment()
	}
	if s.options.InspectionModelOptions != nil {
		env = s.options.InspectionModelOptions.Environment
	}
	values := []any{}
	for _, key := range []string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS", "CLAUDE_CODE_DISABLE_1M_CONTEXT", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "USER_TYPE", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		if raw, ok := env[key]; ok {
			values = append(values, raw)
		} else {
			values = append(values, nil)
		}
	}
	value.Set("env", recoveryJSON(values))
	sum := sha256.Sum256(recoveryJSON(value))
	return hex.EncodeToString(sum[:])
}

// Source shares one uncancellable snapshot Promise per native stat/provider
// revision. A cancelled HTTP poll does not cancel another reader's snapshot.
func (s *Server) getInspectionTranscript(ctx context.Context, info sessionInfo) (*inspectionTranscriptSnapshot, error) {
	if info.memory || info.ephemeral {
		return nil, nil
	}
	if s.options.InspectionModelOptions == nil {
		return nil, unsupported("inspection actual model accounting")
	}
	version, err := sessionstore.InspectionVersion(info.Path)
	if err != nil {
		return nil, err
	}
	key := info.Path + ":" + version + ":" + s.inspectionContextRevision(context.WithoutCancel(ctx))
	s.mu.Lock()
	if pending := s.inspectionSnapshots[key]; pending != nil {
		s.mu.Unlock()
		<-pending.done
		if pending.err != nil {
			return nil, pending.err
		}
		value := copyInspectionSnapshot(pending.value)
		return &value, pending.err
	}
	if s.inspectionSnapshots == nil {
		s.inspectionSnapshots = map[string]*inspectionPending{}
	}
	pending := &inspectionPending{done: make(chan struct{})}
	s.inspectionSnapshots[key] = pending
	s.inspectionSnapshotOrder = append(s.inspectionSnapshotOrder, key)
	for len(s.inspectionSnapshotOrder) > 8 {
		delete(s.inspectionSnapshots, s.inspectionSnapshotOrder[0])
		s.inspectionSnapshotOrder = s.inspectionSnapshotOrder[1:]
	}
	s.mu.Unlock()
	buildCtx := context.WithoutCancel(ctx)
	err = s.budget.With(buildCtx, sessionstore.MetadataLane, func() error {
		value, failure := s.readInspectionTranscript(buildCtx, info, *s.options.InspectionModelOptions)
		if failure != nil {
			return failure
		}
		after, failure := sessionstore.InspectionVersion(info.Path)
		if failure != nil {
			return failure
		}
		if after != version {
			return &apiError{409, "CONFLICT", "Transcript changed during inspection; retry the request"}
		}
		if len(recoveryJSON(value)) > 1024*1024 {
			return &apiError{413, "HISTORY_INSPECTION_LIMIT", "Transcript inspection exceeds its display budget"}
		}
		pending.value = value
		return nil
	})
	pending.err = err
	s.mu.Lock()
	if err != nil && s.inspectionSnapshots[key] == pending {
		delete(s.inspectionSnapshots, key)
		for i, value := range s.inspectionSnapshotOrder {
			if value == key {
				s.inspectionSnapshotOrder = append(s.inspectionSnapshotOrder[:i], s.inspectionSnapshotOrder[i+1:]...)
				break
			}
		}
	}
	close(pending.done)
	s.mu.Unlock()
	value := copyInspectionSnapshot(pending.value)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (s *Server) inspectionMissing(id string) error {
	return notFound(fmt.Sprintf("Session not found: %s", id))
}
