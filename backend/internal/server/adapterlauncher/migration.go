package adapterlauncher

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

func migrationCommand(input io.Reader, token string) (string, error) {
	buffer := ""
	chunk := make([]byte, 4096)
	for {
		n, err := input.Read(chunk)
		buffer += string(chunk[:n])
		if len(buffer) > 8192 {
			buffer = ""
		} else {
			lines := strings.Split(buffer, "\n")
			buffer = lines[len(lines)-1]
			for _, line := range lines[:len(lines)-1] {
				var request struct {
					Type      *string `json:"type"`
					Token     *string `json:"token"`
					RequestID *string `json:"requestId"`
				}
				if json.Unmarshal([]byte(line), &request) != nil || request.Type == nil || *request.Type != "migration_quiesce" || request.Token == nil || request.RequestID == nil {
					continue
				}
				if len(*request.Token) == len(token) && subtle.ConstantTimeCompare([]byte(*request.Token), []byte(token)) == 1 {
					return *request.RequestID, nil
				}
			}
		}
		if err != nil {
			return "", err
		}
	}
}

func migrationReply(output io.Writer, requestID string, failure error) error {
	typeName := "migration_quiesced"
	if failure != nil {
		typeName = "migration_quiesce_failed"
	}
	return json.NewEncoder(output).Encode(map[string]string{"type": typeName, "requestId": requestID})
}

type migrationProcess interface{ Quiesce(context.Context) error }

func quiesceProcesses(ctx context.Context, processes []Process) error {
	errorsChannel := make(chan error, len(processes))
	var workers sync.WaitGroup
	for _, process := range processes {
		workers.Add(1)
		go func(process Process) {
			defer workers.Done()
			defer func() {
				if p := recover(); p != nil {
					errorsChannel <- fmt.Errorf("Adapter shutdown failed: %v", p)
				}
			}()
			if owned, ok := process.(migrationProcess); ok {
				if err := owned.Quiesce(ctx); err != nil {
					errorsChannel <- err
				}
			} else {
				process.Close()
			}
		}(process)
	}
	workers.Wait()
	close(errorsChannel)
	var failures []error
	for err := range errorsChannel {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
