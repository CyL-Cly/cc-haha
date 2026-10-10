package cliauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"io"
	"net/http"
)

func loginOpenAI(ctx context.Context, _ LoginInput, runtime LoginRuntime) (Result, error) {
	if runtime.SaveOpenAI == nil || runtime.InstallOpenAI == nil {
		return Result{}, fmt.Errorf("CLI OpenAI login requires actual token installer")
	}
	random := runtime.Random
	if random == nil {
		random = rand.Reader
	}
	verifier, e := randomOpenAIValue(random, 64)
	if e != nil {
		return Result{}, e
	}
	state, e := randomOpenAIValue(random, 32)
	if e != nil {
		return Result{}, e
	}
	listener := NewOAuthListener(desktopoauth.OpenAICodexRedirectPath)
	listener.SetErrorRedirect(claudeSuccessURL(runtime.Client.Config))
	browserContext, cancelBrowser := context.WithCancel(ctx)
	defer func() { cancelBrowser(); listener.Close() }()
	port, e := listener.Start(desktopoauth.OpenAICodexOAuthPort)
	if e != nil {
		return Result{ExitCode: 1, Stderr: "OpenAI login failed: " + e.Error() + "\n"}, nil
	}
	redirect := fmt.Sprintf("http://localhost:%d%s", port, desktopoauth.OpenAICodexRedirectPath)
	authorize := desktopoauth.BuildOpenAIAuthorizeURL(redirect, verifier, state)
	code, e := listener.Wait(ctx, state, func() error {
		if e := runtime.Write("Opening browser to sign in to OpenAI…\nIf the browser did not open, visit: " + authorize + "\n"); e != nil {
			return e
		}
		_ = runtime.Browser(browserContext, authorize)
		return nil
	})
	if e != nil {
		return Result{ExitCode: 1, Stderr: "OpenAI login failed: " + e.Error() + "\n"}, nil
	}
	tokens, e := runtime.Client.ExchangeOpenAI(ctx, code, redirect, verifier)
	if e == nil {
		e = runtime.SaveOpenAI(ctx, tokens)
	}
	if e != nil {
		listener.Respond(200, http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, openAILoginErrorHTML)
		return Result{ExitCode: 1, Stderr: "OpenAI login failed: " + e.Error() + "\n"}, nil
	}
	listener.Respond(200, http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, openAILoginSuccessHTML)
	cancelBrowser()
	listener.Close()
	warning, e := runtime.InstallOpenAI(ctx, tokens)
	if e != nil {
		return Result{ExitCode: 1, Stderr: "OpenAI login failed: " + e.Error() + "\n"}, nil
	}
	out := "OpenAI login successful.\n"
	if warning != "" {
		out += warning + "\n"
	}
	return Result{Stdout: out}, nil
}

func randomOpenAIValue(reader io.Reader, count int) (string, error) {
	data := make([]byte, count)
	if _, e := io.ReadFull(reader, data); e != nil {
		return "", e
	}
	return hex.EncodeToString(data), nil
}
