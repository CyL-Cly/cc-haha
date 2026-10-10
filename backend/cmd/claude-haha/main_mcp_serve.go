package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpcli"
)

func serveNativeMCP(ctx context.Context, command *clibootstrap.MainCommand, platform string, input *os.File, output, diagnostics io.Writer, debug, verbose bool) error {
	if input == nil {
		return fmt.Errorf("MCP serve requires actual standard input")
	}
	if _, err := os.Stat(command.Native.CWD); err != nil {
		return fmt.Errorf("Error: Directory %s does not exist", command.Native.CWD)
	}
	host, err := clibootstrap.NewNativeMCPToolContext(command, platform, debug, verbose)
	if err != nil {
		return err
	}
	return mcpcli.Serve(ctx, mcpcli.ServeOptions{Version: version, Input: input, Output: output, Host: host, OnTransportError: func(err error) {
		if debug {
			fmt.Fprintln(diagnostics, err)
		}
	}})
}
