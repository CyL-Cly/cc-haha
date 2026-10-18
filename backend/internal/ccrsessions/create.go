package ccrsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/charmbracelet/x/ansi"
)

type Created struct{ ID, Title string }

func (s *Service) CreateRemote(ctx context.Context, description string) (Created, error) {
	ctx, done, err := s.operation(ctx)
	if err != nil {
		return Created{}, err
	}
	defer done()
	allowed, err := s.PolicyRemoteAllowed(ctx)
	if err != nil {
		return Created{}, err
	}
	if !allowed {
		return Created{}, errors.New("Remote sessions are disabled by your organization's policy.")
	}
	if description == "" && !s.options.RemoteBackendEnabled {
		return Created{}, errors.New("Error: --remote requires a description.\nUsage: claude --remote \"your task description\"")
	}
	credentials, err := s.credentials(ctx, true)
	if err != nil {
		return Created{}, err
	}
	titleDescription := description
	if titleDescription == "" {
		titleDescription = "Background task"
	}
	title := Title{ansi.Truncate(titleDescription, 75, "…"), "claude/task"}
	generated, failure := s.options.GenerateTitle(ctx, titleDescription)
	if ctx.Err() != nil {
		return Created{}, context.Cause(ctx)
	}
	if failure == nil {
		if generated.Title != "" {
			title.Title = generated.Title
		}
		if generated.Branch != "" {
			title.Branch = generated.Branch
		}
	} else {
		s.report(failure)
	}
	root, _ := s.git(ctx, "rev-parse", "--show-toplevel")
	gitRoot := ""
	if root.Code == 0 {
		gitRoot = strings.TrimSpace(root.Stdout)
	}
	repository := s.Repository(ctx)
	forceBundle := truthy(s.options.Environment["CCR_FORCE_BUNDLE"])
	bundleEnabled := gitRoot != "" && truthy(s.options.Environment["CCR_ENABLE_BUNDLE"])
	if s.options.Environment["USER_TYPE"] == "ant" && gitRoot != "" {
		var overrides protocol.JSONObject
		if overrides.UnmarshalJSON([]byte(s.options.Environment["CLAUDE_INTERNAL_FC_OVERRIDES"])) == nil && string(overrides.Get("tengu_ccr_bundle_seed_enabled")) == "true" {
			bundleEnabled = true
		}
	}
	viable := false
	if repository != nil && !forceBundle {
		if repository.Host == "github.com" {
			body, status, failure := s.request(ctx, "GET", "/api/oauth/organizations/"+credentials.Organization+"/code/repos/"+repository.Owner+"/"+repository.Name, nil, credentials, false, 15*time.Second, nil)
			if ctx.Err() != nil {
				return Created{}, context.Cause(ctx)
			}
			statusBody, _ := body.Object("status")
			viable = failure == nil && status == 200 && string(statusBody.Get("app_installed")) == "true"
		} else {
			viable = true
		}
	}
	if !viable && !bundleEnabled && repository != nil {
		viable = true
	}
	sources, outcomes := []any{}, []any{}
	seed := ""
	if viable && repository != nil {
		branch, _ := s.git(ctx, "branch", "--show-current")
		revision := strings.TrimSpace(branch.Stdout)
		if revision == "" {
			defaultBranch, _ := s.git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
			revision = strings.TrimPrefix(strings.TrimSpace(defaultBranch.Stdout), "origin/")
			if revision == "" {
				revision = "main"
			}
		}
		sources = append(sources, map[string]any{"type": "git_repository", "url": "https://" + repository.Host + "/" + repository.Owner + "/" + repository.Name, "revision": revision})
		outcomes = append(outcomes, map[string]any{"type": "git_repository", "git_info": map[string]any{"type": "github", "repo": repository.Owner + "/" + repository.Name, "branches": []string{title.Branch}}})
	} else if bundleEnabled {
		seed, err = s.createAndUploadBundle(ctx, credentials, 100*1024*1024)
		if err != nil {
			return Created{}, err
		}
	}
	context := map[string]any{"sources": sources, "outcomes": outcomes, "model": s.options.Model}
	if seed != "" {
		context["seed_bundle_file_id"] = seed
	}
	settings, err := s.options.Settings(ctx)
	if err != nil {
		return Created{}, err
	}
	remote, _ := settings.Object("remote")
	defaultEnvironment := remote.String("defaultEnvironmentId")
	body, status, err := s.request(ctx, "GET", "/v1/environment_providers", nil, credentials, false, 15*time.Second, nil)
	if err != nil || status != 200 {
		return Created{}, fmt.Errorf("Failed to fetch environments: %d %s", status, http.StatusText(status))
	}
	var environments []protocol.JSONObject
	if json.Unmarshal(body.Get("environments"), &environments) != nil || len(environments) == 0 {
		return Created{}, errors.New("No environments available for session creation")
	}
	selected := environments[0]
	for _, environment := range environments {
		if environment.String("kind") != "bridge" {
			selected = environment
			break
		}
	}
	for _, environment := range environments {
		if environment.String("kind") == "anthropic_cloud" {
			selected = environment
			break
		}
	}
	for _, environment := range environments {
		if defaultEnvironment != "" && environment.String("environment_id") == defaultEnvironment {
			selected = environment
			break
		}
	}
	events := []any{}
	if description != "" {
		events = append(events, map[string]any{"type": "event", "data": map[string]any{"uuid": uuid(), "session_id": "", "type": "user", "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": description}}})
	}
	requestBody, _ := json.Marshal(map[string]any{"title": title.Title, "events": events, "session_context": context, "environment_id": selected.String("environment_id")})
	result, status, err := s.request(ctx, "POST", "/v1/sessions", requestBody, credentials, true, 0, nil)
	if err != nil {
		return Created{}, err
	}
	if status != 200 && status != 201 || result.String("id") == "" {
		return Created{}, errors.New("Error: Unable to create remote session")
	}
	if value := result.String("title"); value != "" {
		title.Title = value
	}
	return Created{result.String("id"), title.Title}, nil
}
