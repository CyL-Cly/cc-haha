package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeDingTalkAPI = imruntime.NewDingTalkAPI
var newNativeDingTalkStream = imruntime.NewDingTalkStream

func startNativeDingTalk(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	config := imruntime.LoadPlatformConfig("dingtalk", o.HomeDir, o.WorkDir, o.Environment, func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) })
	runtime, err := imruntime.NewDingTalkRuntime(ctx, imruntime.DingTalkRuntimeOptions{Config: config, NewAPI: newNativeDingTalkAPI, NewStream: newNativeDingTalkStream, Log: func(message string) { _, _ = fmt.Fprintln(out, message) }})
	if err != nil {
		return nil, err
	}
	if err = runtime.Start(); err != nil {
		runtime.Close()
		return nil, err
	}
	return runtime, nil
}
