package clibootstrap

import (
	"context"
	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"sync"
)

type pluginCLIJobs struct {
	ctx    context.Context
	ignore *settings.GitIgnore
	group  sync.WaitGroup
	mu     sync.Mutex
	errors []error
}

func newPluginCLIJobs(ctx context.Context, n *NativeBoundary) (*pluginCLIJobs, error) {
	ignore, err := settings.NewGitIgnore(settings.GitIgnoreOptions{HomeDir: n.Home, Environment: n.nativeEnvironmentSnapshot, Executable: "git"})
	if err != nil {
		return nil, err
	}
	return &pluginCLIJobs{ctx: ctx, ignore: ignore}, nil
}
func (jobs *pluginCLIJobs) StartLocalIgnore(cwd, filename string) error {
	jobs.group.Add(1)
	go func() {
		defer jobs.group.Done()
		if err := jobs.ignore.AddFileGlobRule(jobs.ctx, filename, cwd); err != nil {
			jobs.mu.Lock()
			jobs.errors = append(jobs.errors, err)
			jobs.mu.Unlock()
		}
	}()
	return nil
}
func (jobs *pluginCLIJobs) Join() { jobs.group.Wait() }

func (jobs *pluginCLIJobs) Errors() []string {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	out := []string{}
	for _, failure := range jobs.errors {
		out = append(out, failure.Error())
	}
	return out
}
