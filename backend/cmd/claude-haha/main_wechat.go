package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeWechatAPI = imruntime.NewWechatAPI

func startNativeWechat(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	log := func(message string) { _, _ = fmt.Fprintln(out, message) }
	warn := func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) }
	config := imruntime.LoadPlatformConfig("wechat", o.HomeDir, o.WorkDir, o.Environment, warn)
	if config.Err != nil {
		return nil, config.Err
	}
	credentials := imruntime.WechatConfig(config)
	api, err := newNativeWechatAPI(imruntime.WechatAPIOptions{BaseURL: credentials.BaseURL, Token: credentials.BotToken, Environment: config.Environment})
	if err != nil {
		return nil, err
	}
	runtime, err := imruntime.NewWechatRuntime(ctx, imruntime.WechatRuntimeOptions{Config: config, API: api, Log: log})
	if err != nil {
		api.Close()
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
