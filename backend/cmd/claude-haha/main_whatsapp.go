package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
	wa "github.com/NanmiCoder/cc-haha/backend/internal/whatsappcompat"
)

var newNativeWhatsAppRuntime = imruntime.NewWhatsAppRuntime

func startNativeWhatsApp(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	config := imruntime.LoadPlatformConfig("whatsapp", o.HomeDir, o.WorkDir, o.Environment, func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) })
	if config.Err != nil {
		return nil, config.Err
	}
	runtime, err := newNativeWhatsAppRuntime(ctx, imruntime.WhatsAppRuntimeOptions{
		Config:        config,
		Log:           func(message string) { _, _ = fmt.Fprintln(out, message) },
		ClientOptions: wa.NodeWhatsAppOptions{Warn: func(err error) { _, _ = fmt.Fprintln(o.Stderr, "[WhatsApp] Failed to save credentials:", err) }},
	})
	if err != nil {
		return nil, err
	}
	runtime.Start()
	return runtime, nil
}
