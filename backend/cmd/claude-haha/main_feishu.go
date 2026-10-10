package main

import (
	"context"
	"fmt"

	"github.com/NanmiCoder/cc-haha/backend/internal/imruntime"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/server/adapterlauncher"
)

var newNativeFeishuClient = imruntime.NewFeishuClient
var newNativeFeishuRuntime = imruntime.NewFeishuRuntime

func startNativeFeishu(ctx context.Context, o adapterlauncher.Options) (adapterlauncher.Process, error) {
	out := o.Stdout
	if out == nil {
		out = o.Stderr
	}
	log := func(message string) { _, _ = fmt.Fprintln(out, message) }
	config := imruntime.LoadPlatformConfig("feishu", o.HomeDir, o.WorkDir, o.Environment, func(message string) { _, _ = fmt.Fprintln(o.Stderr, message) })
	if config.Err != nil {
		return nil, config.Err
	}
	credentials := protocol.NewJSONObject(
		protocol.JSONField{Name: "appId", Value: imruntime.PlatformCredential(config, "feishu", "appId", "FEISHU_APP_ID", "")},
		protocol.JSONField{Name: "appSecret", Value: imruntime.PlatformCredential(config, "feishu", "appSecret", "FEISHU_APP_SECRET", "")},
	)
	domain := "feishu"
	if imruntime.TextJSON(imruntime.PlatformCredential(config, "feishu", "domain", "FEISHU_DOMAIN", "")).String() == "lark" {
		domain = "lark"
	}
	client, err := newNativeFeishuClient(ctx, imruntime.FeishuClientOptions{Credentials: credentials, Domain: domain, Environment: config.Environment, Log: log})
	if err != nil {
		return nil, err
	}
	runtime, err := newNativeFeishuRuntime(ctx, imruntime.FeishuRuntimeOptions{
		Config: config, Credentials: credentials, Domain: domain, Client: client, Log: log,
		EncryptKey:        imruntime.TextJSON(imruntime.PlatformCredential(config, "feishu", "encryptKey", "FEISHU_ENCRYPT_KEY", "")),
		VerificationToken: imruntime.TextJSON(imruntime.PlatformCredential(config, "feishu", "verificationToken", "FEISHU_VERIFICATION_TOKEN", "")),
	})
	if err != nil {
		client.Close()
		return nil, err
	}
	if err = runtime.Start(); err != nil {
		runtime.Close()
		return nil, err
	}
	return runtime, nil
}
