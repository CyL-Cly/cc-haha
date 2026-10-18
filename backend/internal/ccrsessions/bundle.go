package ccrsessions

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func uuid() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	data[6], data[8] = data[6]&15|64, data[8]&63|128
	s := hex.EncodeToString(data[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func (s *Service) createAndUploadBundle(ctx context.Context, credentials Credentials, maxBytes int64) (string, error) {
	for _, ref := range []string{"refs/seed/stash", "refs/seed/root"} {
		_, _ = s.git(ctx, "update-ref", "-d", ref)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, ref := range []string{"refs/seed/stash", "refs/seed/root"} {
			_, _ = s.runGit(cleanup, "update-ref", "-d", ref)
		}
	}()
	refs, err := s.git(ctx, "for-each-ref", "--count=1", "refs/")
	if err != nil {
		return "", err
	}
	if refs.Code == 0 && strings.TrimSpace(refs.Stdout) == "" {
		return "", errors.New("Repository has no commits — run `git add . && git commit -m \"initial\"` then retry")
	}
	stash, err := s.git(ctx, "stash", "create")
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	wip := ""
	if err == nil && stash.Code == 0 {
		wip = strings.TrimSpace(stash.Stdout)
	}
	if wip != "" {
		_, _ = s.git(ctx, "update-ref", "refs/seed/stash", wip)
	}
	file, err := os.CreateTemp(s.options.TempDir, "ccr-seed-*.bundle")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_ = file.Close()
	defer os.Remove(path)
	extra := []string{}
	if wip != "" {
		extra = append(extra, "refs/seed/stash")
	}
	var content []byte
	for _, base := range []string{"--all", "HEAD", "refs/seed/root"} {
		if base == "refs/seed/root" {
			tree := "HEAD^{tree}"
			if wip != "" {
				tree = "refs/seed/stash^{tree}"
			}
			root, err := s.git(ctx, "commit-tree", tree, "-m", "seed")
			if err != nil || root.Code != 0 {
				return "", fmt.Errorf("Failed to create git bundle (git commit-tree failed (%d): %s)", root.Code, root.Stderr)
			}
			_, _ = s.git(ctx, "update-ref", "refs/seed/root", strings.TrimSpace(root.Stdout))
			extra = nil
		}
		args := append([]string{"bundle", "create", path, base}, extra...)
		result, err := s.git(ctx, args...)
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		if err != nil || result.Code != 0 {
			return "", fmt.Errorf("Failed to create git bundle (git bundle create %s failed (%d): %s)", base, result.Code, result.Stderr)
		}
		content, err = os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if int64(len(content)) <= maxBytes {
			break
		}
	}
	if int64(len(content)) > maxBytes {
		return "", errors.New("Repo is too large to teleport. Please setup GitHub on https://claude.ai/code")
	}
	boundary := "----FormBoundary" + uuid()
	var body bytes.Buffer
	_, _ = fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"_source_seed.bundle\"\r\nContent-Type: application/octet-stream\r\n\r\n", boundary)
	body.Write(content)
	_, _ = fmt.Fprintf(&body, "\r\n--%s\r\nContent-Disposition: form-data; name=\"purpose\"\r\n\r\nuser_data\r\n--%s--\r\n", boundary, boundary)
	headers := http.Header{"Content-Type": []string{"multipart/form-data; boundary=" + boundary}, "anthropic-beta": []string{"files-api-2025-04-14,oauth-2025-04-20"}}
	credentials.Organization = ""
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		response, status, err := s.request(ctx, "POST", "/v1/files", body.Bytes(), credentials, false, 120*time.Second, headers)
		if err == nil && (status == 200 || status == 201) && response.String("id") != "" {
			return response.String("id"), nil
		}
		if ctx.Err() != nil {
			return "", context.Cause(ctx)
		}
		if status == 401 || status == 403 {
			return "", fmt.Errorf("Bundle upload failed: status %d", status)
		}
		last = err
		if last == nil {
			last = fmt.Errorf("status %d", status)
		}
		if attempt < 2 {
			if err := s.options.Wait(ctx, 500*time.Millisecond*time.Duration(1<<attempt)); err != nil {
				return "", err
			}
		}
	}
	return "", fmt.Errorf("Bundle upload failed: %v after 3 attempts", last)
}
