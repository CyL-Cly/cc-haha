package main

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeSlackAPI = imruntime.NewSlackAPI

func startNativeSlack(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	log := func(s string) { _, _ = fmt.Fprintln(out, s) }
	warn := func(s string) { _, _ = fmt.Fprintln(o.Stderr, s) }
	config := imruntime.LoadPlatformConfig("slack", o.HomeDir, o.WorkDir, o.Environment, warn)
	if config.Err != nil {
		return nil, config.Err
	}
	api, err := newNativeSlackAPI(imruntime.SlackAPIOptions{Token: config.Token, Environment: config.Environment})
	if err != nil {
		return nil, err
	}
	runtime, err := imruntime.NewSlackRuntime(ctx, imruntime.SlackRuntimeOptions{Config: config, API: api, Log: log})
	if err != nil {
		api.Close()
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
