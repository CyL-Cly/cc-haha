package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeQQClient = imruntime.NewQQClient

func startNativeQQ(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	config := imruntime.LoadPlatformConfig("qq", o.HomeDir, o.WorkDir, o.Environment, func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) })
	runtime, err := imruntime.NewQQRuntime(ctx, imruntime.QQRuntimeOptions{Config: config, NewClient: newNativeQQClient, Log: func(message string) { _, _ = fmt.Fprintln(out, message) }})
	if err != nil {
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
