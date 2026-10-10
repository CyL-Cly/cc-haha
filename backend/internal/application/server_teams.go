package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamplan"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamreader"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

func newDesktopTeams(o ServerOptions, s *server.Server, native *clibootstrap.NativeBoundary, providers *providerconfig.Service) error {
	if s == nil || native == nil || providers == nil {
		return errors.New("Desktop Teams requires its actual Server/native/provider factory")
	}
	log := func(err error) { _, _ = fmt.Fprintf(o.Stderr, "[Teams] %v\n", err) }
	tasks, err := taskstore.New(taskstore.Options{ConfigDir: o.ConfigDir, PID: os.Getpid(), Now: native.Now})
	if err != nil {
		return err
	}
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
	}
	config, err := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: o.ConfigDir, TaskStore: tasks, PID: os.Getpid(), Platform: platform, Log: log})
	if err != nil {
		return err
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: o.ConfigDir, TaskStore: tasks})
	if err != nil {
		return err
	}
	plans, err := teamplan.New(teamplan.Options{Config: config, Tasks: tasks, ConfigDir: o.ConfigDir, WorkDir: o.WorkDir, Now: native.Now, UUID: nativeDesktopTeamUUID})
	if err != nil {
		return err
	}
	runtimeOptions := teamworker.RuntimeOptions{Config: config, Plans: plans, Tasks: tasks, Mailbox: mailbox, Validation: teamworker.ValidationOptions{Routing: providers.TeamRuntime}, Now: native.Now, UUID: nativeDesktopTeamUUID, Log: log}
	s.BindTeamSessionCallbacks(&runtimeOptions)
	runtime, err := teamworker.NewRuntime(runtimeOptions)
	if err != nil {
		return err
	}
	approval, err := teamplan.NewService(plans, runtime.ServiceBindings())
	if err != nil {
		_ = runtime.Close(context.Background())
		return err
	}
	resources, err := clibootstrap.NativeTeamResourceCleanup(native, func(level, message string) {
		if level == "error" {
			_, _ = fmt.Fprintln(o.Stderr, message)
		}
	})
	if err != nil {
		_ = approval.Close(context.Background())
		_ = runtime.Close(context.Background())
		return err
	}
	readerOptions := teamreader.Options{ConfigDir: o.ConfigDir, Tasks: tasks, Mailbox: mailbox, Now: native.Now, CleanupTeam: func(ctx context.Context, name string, scoped *taskstore.Service) error {
		lifecycle, err := teamstore.NewLifecycle(teamstore.LifecycleOptions{Config: config, Tasks: scoped, RuntimeResources: resources})
		if err != nil {
			return err
		}
		_, err = lifecycle.Cleanup(ctx, name, nil, false)
		return err
	}}
	s.BindTeamReader(&readerOptions)
	reader, err := teamreader.New(readerOptions)
	if err != nil {
		_ = approval.Close(context.Background())
		_ = runtime.Close(context.Background())
		return err
	}
	api, err := teamsapi.New(teamsapi.Options{Plans: approval, Reader: reader})
	if err == nil {
		err = s.BindTeams(api, approval, runtime)
	}
	if err == nil {
		err = s.BindTeamWatcher(reader)
	}
	if err != nil {
		_ = approval.Close(context.Background())
		_ = runtime.Close(context.Background())
	}
	return err
}
