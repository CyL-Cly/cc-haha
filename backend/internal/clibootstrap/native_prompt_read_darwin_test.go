//go:build darwin

package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestNativePromptPhysicalReadKeepsOriginalFilesAndErrors(t *testing.T) {
	home := t.TempDir()
	for name, body := range map[string][]byte{"empty": {}, "bytes": {0, 0xff, 'a', '\n'}} {
		if err := os.WriteFile(filepath.Join(home, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "bytes"), filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"empty", "bytes", "link", "missing", "."} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(home, name)
			want, originalErr := os.ReadFile(path)
			got, err := nativePromptReadFile(context.Background(), path)
			if !bytes.Equal(got, want) || (err == nil) != (originalErr == nil) {
				t.Fatal("physical read differs from original os.ReadFile", got, want, err, originalErr)
			}
			if originalErr != nil {
				var wantPath *os.PathError
				if !errors.As(originalErr, &wantPath) || !errors.Is(err, wantPath.Err) {
					t.Fatal("physical read changed the original error category", err, originalErr)
				}
			}
		})
	}
}

func TestNativePromptPhysicalFIFOPreservesBodyAndCancelsEnteredRead(t *testing.T) {
	for _, cancelEntered := range []bool{false, true} {
		name := "body"
		if cancelEntered {
			name = "cancel-entered"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "actual-user.fifo")
			if err := syscall.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			type readResult struct {
				body []byte
				err  error
			}
			done := make(chan readResult, 1)
			go func() { body, err := nativePromptReadFile(ctx, path); done <- readResult{body, err} }()
			type openedWriter struct {
				file *os.File
				err  error
			}
			opened := make(chan openedWriter, 1)
			go func() { file, err := os.OpenFile(path, os.O_WRONLY, 0600); opened <- openedWriter{file, err} }()
			var writer *os.File
			openedConsumed, joined := false, false
			defer func() {
				cancel()
				if !openedConsumed {
					// Cleanup-only reader releases this fixture's real blocking
					// writer if the physical consumer failed before opening FIFO.
					rescue, _ := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0600)
					result := <-opened
					writer = result.file
					if rescue != nil {
						_ = rescue.Close()
					}
				}
				if writer != nil {
					_ = writer.Close()
				}
				if !joined {
					<-done
				}
			}()
			select {
			case result := <-opened:
				openedConsumed, writer = true, result.file
				if result.err != nil {
					t.Fatal(result.err)
				}
			case result := <-done:
				joined = true
				t.Fatal("read completed before the original FIFO had a writer", result)
			case <-ctx.Done():
				t.Fatal("actual reader did not open the private FIFO", ctx.Err())
			}
			body := []byte{'o', 'r', 'i', 'g', 'i', 'n', 'a', 'l', 0, 0xff}
			if cancelEntered {
				// Successful O_WRONLY open proves the actual consumer's read
				// descriptor exists. Leave that real writer connected and empty.
				cancel()
			} else {
				if _, err := writer.Write(body); err != nil {
					t.Fatal(err)
				}
				_ = writer.Close()
			}
			select {
			case result := <-done:
				joined = true
				if cancelEntered {
					if !errors.Is(result.err, context.Canceled) {
						t.Fatal("entered physical read did not retire on producer close", result.err)
					}
				} else if result.err != nil || !bytes.Equal(result.body, body) {
					t.Fatal("actual FIFO body changed", result.body, result.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("actual entered FIFO read did not settle and join")
			}
		})
	}
}
