package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

// Tests replace only the native Telegram HTTP endpoint/client, while executing
// the public adapters route, actual configuration and owned runtime unchanged.
var newNativeTelegramAPI = imruntime.NewTelegramAPI

func startNativeTelegram(ctx context.Context, options adapterlauncher.Options) (adapterlauncher.Process, error) {
	output := options.Stdout
	if output == nil {
		output = options.Stderr
	}
	log := func(message string) { _, _ = fmt.Fprintln(output, message) }
	warn := func(message string) { _, _ = fmt.Fprintln(options.Stderr, message) }
	config := imruntime.LoadConfig(options.HomeDir, options.WorkDir, options.Environment, warn)
	if config.Err != nil {
		return nil, config.Err
	}
	api, err := newNativeTelegramAPI(imruntime.TelegramAPIOptions{Token: config.Token, Environment: config.Environment})
	if err != nil {
		return nil, err
	}
	runtime, err := imruntime.NewTelegramRuntime(ctx, imruntime.TelegramRuntimeOptions{Config: config, API: api, Log: log, Delivery: imruntime.TelegramDeliveryOptions{Warn: warn}})
	if err != nil {
		api.Close()
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
