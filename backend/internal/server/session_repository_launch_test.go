package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
)

func TestSessionRepositoryHTTPPlansWithoutMaterializingAndValidatesOriginalFields(t *testing.T) {
	s, o := repositoryLaunchFixture(t)
	h := httptest.NewServer(s)
	defer h.Close()
	for _, row := range []struct{ body, message string }{{`{"repository":null}`, "repository must be an object"}, {`{"repository":[]}`, "repository must be an object"}, {`{"repository":{"branch":false}}`, "repository.branch must be a string"}, {`{"repository":{"worktree":null}}`, "repository.worktree must be a boolean"}, {`{"repository":{"worktree":"true"}}`, "repository.worktree must be a boolean"}, {`{"permissionMode":null}`, "permissionMode must be a string"}} {
		raw := requestJSON(t, h, "POST", "/api/sessions", row.body, 400)
		var errorValue map[string]any
		_ = json.Unmarshal(raw, &errorValue)
		if errorValue["message"] != row.message {
			t.Fatal(row.body, string(raw))
		}
	}
	body, _ := json.Marshal(map[string]any{"workDir": o.WorkDir, "repository": map[string]any{"branch": "feature", "worktree": true}, "permissionMode": "default"})
	data := requestJSON(t, h, "POST", "/api/sessions", string(body), 201)
	var created struct{ SessionID, WorkDir string }
	_ = json.Unmarshal(data, &created)
	info, e := s.findSession(context.Background(), created.SessionID)
	if e != nil {
		t.Fatal(e)
	}
	var metadata struct{ Repository sessionRepository }
	_ = json.Unmarshal(info.Metadata, &metadata)
	if metadata.Repository.Branch != "feature" || metadata.Repository.BaseRef != "feature" || metadata.Repository.WorktreeSlug != "desktop-feature-"+created.SessionID[:8] || created.WorkDir != o.WorkDir {
		t.Fatal(string(info.Metadata), string(data))
	}
	if _, e = os.Stat(metadata.Repository.WorktreePath); !os.IsNotExist(e) {
		t.Fatal("API creates worktree before actual launch", e)
	}
	if branch := s.repositoryGit(context.Background(), o.WorkDir, "branch", "--show-current"); trimJS(branch.stdout) != "main" {
		t.Fatal("API switched checkout", branch)
	}
	file := filepath.Join(o.ConfigDir, "projects", projectpath.Sanitize(o.WorkDir), created.SessionID+".jsonl")
	lines, e := os.ReadFile(file)
	if e != nil || len(strings.Split(strings.TrimSpace(string(lines)), "\n")) != 2 {
		t.Fatal("original placeholder entries", e, string(lines))
	}
	requestJSON(t, h, "GET", "/api/sessions/repository-branch", "", 405)
	createdBranch := requestJSON(t, h, "POST", "/api/sessions/repository-branch", string(mustRepositoryJSON(map[string]any{"workDir": o.WorkDir, "name": "http-created", "from": "feature"})), 201)
	if !strings.Contains(string(createdBranch), `"branch":"http-created"`) {
		t.Fatal(string(createdBranch))
	}
	if branch := s.repositoryGit(context.Background(), o.WorkDir, "branch", "--show-current"); trimJS(branch.stdout) != "main" {
		t.Fatal("branch API moved HEAD", branch)
	}
	requestJSON(t, h, "POST", "/api/sessions/repository-branch", string(mustRepositoryJSON(map[string]any{"workDir": o.WorkDir, "name": "http-created"})), 400)
}
func mustRepositoryJSON(value any) []byte { b, _ := json.Marshal(value); return b }
