package clibootstrap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

// FetchTerminalPRStatus is the ordinary footer's read-only gh consumer. Git
// discovery and branch promises belong to the same process Owner and watcher
// as ASK; the terminal lifetime owns the five-second native process worker.
func (m *MainPrint) FetchTerminalPRStatus(ctx context.Context) (json.RawMessage, error) {
	result := json.RawMessage("null")
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed || m.Host == nil {
			return cooperative.ErrClosed
		}
		if m.nativeTerminalPRIsGit == nil {
			root, err := m.Git.FindRoot(scope, m.workingDirectory())
			if err != nil {
				return err
			}
			isGit := root != ""
			m.nativeTerminalPRIsGit = &isGit
		}
		if !*m.nativeTerminalPRIsGit {
			return nil
		}
		branchFuture, err := m.Git.Branch(scope)
		if err != nil {
			return err
		}
		defaultFuture, err := m.Git.DefaultBranch(scope)
		if err != nil {
			return err
		}
		branch, err := branchFuture.Await(scope)
		if err != nil {
			return err
		}
		defaultBranch, err := defaultFuture.Await(scope)
		if err != nil {
			return err
		}
		if branch == defaultBranch {
			return nil
		}
		environment, cwd := m.nativeOpenAIEnvironment(), m.workingDirectory()
		future, err := nativeSkillWork(m, scope, func() (worktree.ProcessResult, error) {
			active, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return worktree.NativeRun(active, worktree.ProcessRequest{Command: "gh", Arguments: []string{"pr", "view", "--json", "number,url,reviewDecision,isDraft,headRefName,state"}, CWD: cwd, Environment: environment, IgnoreStdin: true})
		})
		if err != nil {
			return err
		}
		value, err := future.Await(scope)
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err != nil || value.Code != 0 || protocol.TrimJavaScriptString(value.Stdout) == "" {
			return nil
		}
		result = terminalPRStatusFromGH([]byte(value.Stdout), defaultBranch)
		return nil
	})
	if err == nil {
		err = receipt.Wait()
	}
	return result, err
}

func terminalPRStatusFromGH(data []byte, defaultBranch string) json.RawMessage {
	normalized, err := protocol.ParseAndStringifyJavaScriptJSON(data)
	if err != nil || string(normalized) == "null" {
		return json.RawMessage("null")
	}
	var value protocol.JSONObject
	if value.UnmarshalJSON(normalized) != nil {
		// JavaScript property reads on non-null primitives/arrays yield
		// undefined for these fields; Source still returns pending review.
		return rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "reviewState", Value: quoteMain("pending")}))
	}
	head := value.String("headRefName")
	if head == defaultBranch || head == "main" || head == "master" || value.String("state") == "MERGED" || value.String("state") == "CLOSED" {
		return json.RawMessage("null")
	}
	review := "pending"
	if nativeSettingsTruthy(value.Get("isDraft")) {
		review = "draft"
	} else {
		switch value.String("reviewDecision") {
		case "APPROVED":
			review = "approved"
		case "CHANGES_REQUESTED":
			review = "changes_requested"
		}
	}
	result := protocol.NewJSONObject()
	for _, key := range []string{"number", "url"} {
		if value.Get(key) != nil {
			result.Set(key, value.Get(key))
		}
	}
	result.Set("reviewState", quoteMain(review))
	return rawMain(result)
}
