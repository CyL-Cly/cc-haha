package pluginsapi

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

func (r *MarketplaceResolver) refreshGit(ctx context.Context, source protocol.JSONObject, location string, progress func(string)) error {
	kind := source.String("source")
	gitURL := source.String("url")
	if kind == "git" {
		return r.gitCache(ctx, gitURL, location, source, progress)
	}
	first, second := "https://github.com/"+source.String("repo")+".git", "git@github.com:"+source.String("repo")+".git"
	remote := strings.ToLower(strings.TrimSpace(r.options.Environment["CLAUDE_CODE_REMOTE"]))
	if remote == "1" || remote == "true" || remote == "yes" || remote == "on" {
		return r.gitCache(ctx, first, location, source, progress)
	}
	if r.sshConfigured(ctx) {
		first, second = second, first
	}
	if err := r.gitCache(ctx, first, location, source, progress); err != nil {
		return r.gitCache(ctx, second, location, source, progress)
	}
	return nil
}
