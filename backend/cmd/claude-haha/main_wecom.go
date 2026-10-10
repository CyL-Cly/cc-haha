package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeWecomClient = imruntime.NewWecomClient

func startNativeWecom(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	config := imruntime.LoadPlatformConfig("wecom", o.HomeDir, o.WorkDir, o.Environment, func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) })
	runtime, err := imruntime.NewWecomRuntime(ctx, imruntime.WecomRuntimeOptions{Config: config, NewClient: newNativeWecomClient, Log: func(message string) { _, _ = fmt.Fprintln(out, message) }})
	if err != nil {
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
