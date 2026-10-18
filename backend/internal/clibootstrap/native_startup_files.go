package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/remoteio"
)

type startupFileSpec struct{ fileID, relativePath string }
type startupFileResult struct {
	fileID, path string
	bytes        int
	err          error
}

// The promise belongs to the process, not an ASK turn. Closing startup or the
// runtime cancels in-flight HTTP and joins workers before any home is released.
type nativeStartupFiles struct {
	cancel  context.CancelFunc
	done    chan struct{}
	results []startupFileResult
	once    sync.Once
}

func parseStartupFileSpecs(specs []string) []startupFileSpec {
	var result []startupFileSpec
	for _, argument := range specs {
		// Source splits literal spaces only (sandbox-gateway can join specs).
		for _, spec := range strings.Split(argument, " ") {
			id, path, ok := strings.Cut(spec, ":")
			if ok && id != "" && path != "" {
				result = append(result, startupFileSpec{id, path})
			}
		}
	}
	return result
}

func startupFilePath(base, sid, relative string) string {
	normalized := filepath.Clean(relative)
	// Preserve Source's startsWith('..') rule, including '..name'.
	if strings.HasPrefix(normalized, "..") {
		return ""
	}
	uploads := filepath.Join(base, sid, "uploads")
	for _, prefix := range []string{uploads + string(filepath.Separator), string(filepath.Separator) + "uploads" + string(filepath.Separator)} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimPrefix(normalized, prefix)
			break
		}
	}
	return filepath.Join(uploads, normalized)
}

func startupFileWait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func downloadStartupFile(ctx context.Context, client *http.Client, base, token, id string, wait func(context.Context, time.Duration) error) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= 3; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, provider.NormalizeAnthropicBaseURL(base)+"/v1/files/"+id+"/content", nil)
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("anthropic-version", "2023-06-01")
			request.Header.Set("anthropic-beta", "files-api-2025-04-14,oauth-2025-04-20")
			var response *http.Response
			response, err = client.Do(request)
			if err == nil {
				content, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				err = readErr
				if readErr == nil {
					switch response.StatusCode {
					case 200:
						cancel()
						return content, nil
					case 404:
						cancel()
						return nil, fmt.Errorf("File not found: %s", id)
					case 401:
						cancel()
						return nil, errors.New("Authentication failed: invalid or missing API key")
					case 403:
						cancel()
						return nil, fmt.Errorf("Access denied to file: %s", id)
					default:
						err = fmt.Errorf("status %d", response.StatusCode)
					}
				}
			}
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
		if attempt < 3 {
			if err := wait(ctx, 500*time.Millisecond*time.Duration(1<<(attempt-1))); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("%v after 3 attempts", last)
}

func beginStartupFiles(n *NativeBoundary, files []startupFileSpec, sid, base, token string, client *http.Client, wait func(context.Context, time.Duration) error) *nativeStartupFiles {
	ctx, cancel := context.WithCancel(context.Background())
	job := &nativeStartupFiles{cancel: cancel, done: make(chan struct{}), results: make([]startupFileResult, len(files))}
	go func() {
		defer close(job.done)
		defer client.CloseIdleConnections()
		var workers sync.WaitGroup
		var mu sync.Mutex
		next := 0
		for worker := 0; worker < 5 && worker < len(files); worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for {
					mu.Lock()
					index := next
					next++
					mu.Unlock()
					if index >= len(files) {
						return
					}
					file := files[index]
					result := startupFileResult{fileID: file.fileID, path: startupFilePath(n.CWD, sid, file.relativePath)}
					if result.path == "" {
						result.err = fmt.Errorf("Invalid file path: %s", file.relativePath)
					} else {
						content, err := downloadStartupFile(ctx, client, base, token, file.fileID, wait)
						if err == nil {
							err = ctx.Err()
						}
						if err == nil {
							err = n.check("mkdirAll", filepath.Dir(result.path))
						}
						if err == nil {
							err = os.MkdirAll(filepath.Dir(result.path), 0755)
						}
						if err == nil {
							err = n.check("writeFile", result.path)
						}
						if err == nil {
							err = os.WriteFile(result.path, content, 0644)
						}
						result.err, result.bytes = err, len(content)
					}
					job.results[index] = result
				}
			}()
		}
		workers.Wait()
	}()
	return job
}

func (m *MainPrint) startCLIStartupFiles(sid string) error {
	if m.agentChild || len(m.options.Arguments.FileSpecs) == 0 {
		return nil
	}
	token := m.options.SessionIngressAuth.Token()
	if token == "" {
		return errors.New("Error: Session token required for file downloads. CLAUDE_CODE_SESSION_ACCESS_TOKEN must be set.")
	}
	files := parseStartupFileSpecs(m.options.Arguments.FileSpecs)
	if len(files) == 0 {
		return nil
	}
	n := m.options.Native
	if remote := n.Environment["CLAUDE_CODE_REMOTE_SESSION_ID"]; remote != "" {
		sid = remote
	} else if m.options.Arguments.SessionID != "" {
		// Source starts downloads before setup applies --session-id; its
		// bootstrap ID, rather than that later supplied ID, owns uploads.
		sid = uuidMain()
	}
	base := n.Environment["ANTHROPIC_BASE_URL"]
	if base == "" {
		config, err := desktopoauth.ResolveConfig(n.Environment)
		if err != nil {
			return err
		}
		base = config.BaseAPIURL
	}
	client, err := m.nativeRemoteHTTP(n.Environment)
	if err != nil {
		return err
	}
	m.startupFiles = beginStartupFiles(n, files, sid, base, token, client, startupFileWait)
	return nil
}

func (m *MainPrint) CLIIngressAuth() *remoteio.IngressAuth { return m.options.SessionIngressAuth }

// Teleport has one initial --file promise across its API/checkout startup and
// local Harness. Move ownership after local construction; never download twice.
func (m *MainPrint) AdoptCLIStartupFiles(parent *MainPrint) {
	if parent != nil && m.startupFiles == nil {
		m.startupFiles, parent.startupFiles = parent.startupFiles, nil
	}
}

func (m *MainPrint) AwaitCLIStartupFiles(ctx context.Context) error {
	job := m.startupFiles
	if job == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-job.done:
	}
	failed := 0
	for _, result := range job.results {
		if result.err != nil {
			failed++
		}
	}
	if failed > 0 && m.options.Error != nil {
		_, err := fmt.Fprintf(m.options.Error, "Warning: %d/%d file(s) failed to download.\n", failed, len(job.results))
		return err
	}
	return nil
}

func (m *MainPrint) closeCLIStartupFiles() {
	if job := m.startupFiles; job != nil {
		job.once.Do(func() {
			job.cancel()
			<-job.done
		})
	}
}
