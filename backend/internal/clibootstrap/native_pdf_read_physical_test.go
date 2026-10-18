package clibootstrap

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// A small valid PDF is generated without an interpreter or remote asset. Its
// page objects/xref are consumed by the real installed pdfinfo and pdftoppm.
func writePhysicalPDFFixture(t *testing.T, path string, pages int) []byte {
	t.Helper()
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", ""}
	kids := []string{}
	for page := 0; page < pages; page++ {
		index := len(objects) + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", index))
		contents := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (PHYSICAL_PDF_PAGE_%d) Tj ET\n", page+1)
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> /Contents %d 0 R >>", index+1), fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(contents), contents))
	}
	objects[1] = fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", pages, strings.Join(kids, " "))
	var data bytes.Buffer
	data.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for index, object := range objects {
		offsets = append(offsets, data.Len())
		fmt.Fprintf(&data, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := data.Len()
	fmt.Fprintf(&data, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&data, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&data, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), data.Bytes()...)
}
func requirePhysicalPoppler(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("CC_HAHA_TEST_POPPLER_BIN")
	explicit := bin != ""
	if !explicit {
		bin = "/opt/homebrew/bin"
	} else if !filepath.IsAbs(bin) {
		t.Fatal("CC_HAHA_TEST_POPPLER_BIN must name an absolute actual Poppler directory")
	}
	for _, name := range []string{"pdfinfo", "pdftoppm"} {
		info, err := os.Stat(filepath.Join(bin, name))
		if err != nil {
			if explicit {
				t.Fatal("explicit actual Poppler dependency is unavailable", name, err)
			}
			t.Skip("actual Poppler required")
		}
		if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			t.Fatal("actual Poppler dependency is not executable", name)
		}
	}
	return bin
}

func TestActualMainPrintPDFReadNativeFilesPagesMessagesAndWriteGuard(t *testing.T) {
	popplerBin := requirePhysicalPoppler(t)
	for _, scenario := range []string{"full-document", "page-range", "too-many-pages", "invalid-pages", "invalid-header"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			var path string
			var original []byte
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				count := calls.Add(1)
				input := map[string]any{"file_path": path}
				if scenario == "page-range" {
					input["pages"] = "2-3"
				}
				if scenario == "invalid-pages" {
					input["pages"] = "1-"
				}
				switch count {
				case 1:
					emitNativeTaskTool(w, "physical-pdf-read", "Read", input)
				case 2:
					var payload protocol.JSONObject
					if err := payload.UnmarshalJSON(body); err != nil {
						t.Error(err)
					}
					var history []protocol.JSONObject
					_ = json.Unmarshal(payload.Get("messages"), &history)
					documents, images := []protocol.JSONObject{}, []protocol.JSONObject{}
					for _, entry := range history {
						var blocks []protocol.JSONObject
						if json.Unmarshal(entry.Get("content"), &blocks) != nil {
							continue
						}
						for _, block := range blocks {
							if block.String("type") == "document" {
								documents = append(documents, block)
							}
							if block.String("type") == "image" {
								images = append(images, block)
							}
						}
					}
					if scenario == "full-document" {
						if len(documents) != 1 || len(images) != 0 {
							t.Error("native document not delivered to next real request", len(documents), len(images))
						} else {
							source, _ := documents[0].Object("source")
							decoded, err := base64.StdEncoding.DecodeString(source.String("data"))
							if err != nil || !bytes.Equal(decoded, original) || source.String("media_type") != "application/pdf" {
								t.Error("full PDF bytes lost in request")
							}
						}
						emitNativeTaskTool(w, "pdf-full-write", "Write", map[string]any{"file_path": path, "content": "ACTUAL_PDF_FULL_READ_AUTHORIZED_WRITE"})
					} else if scenario == "page-range" {
						if len(documents) != 0 || len(images) != 2 {
							t.Error("rendered pages missing or full document leaked", len(documents), len(images))
						}
						for _, block := range images {
							source, _ := block.Object("source")
							decoded, err := base64.StdEncoding.DecodeString(source.String("data"))
							if err != nil || len(decoded) < 100 || source.String("media_type") != "image/jpeg" {
								t.Error("rendered actual JPEG lost")
							}
						}
						emitNativeTaskTool(w, "pdf-part-write", "Write", map[string]any{"file_path": path, "content": "MUST_NOT_REPLACE_AFTER_PAGE_ONLY_READ"})
					} else {
						if len(documents) != 0 || len(images) != 0 {
							t.Error("failed PDF entered model context")
						}
						expected := "File is not a valid PDF (missing %PDF- header)"
						if scenario == "too-many-pages" {
							expected = "This PDF has 11 pages"
						}
						if scenario == "invalid-pages" {
							expected = "exceeds maximum of 20 pages per request"
						}
						if !bytes.Contains(body, []byte(expected)) {
							t.Error("original PDF failure missing in actual next request", expected)
						}
						emitPlanFixture(w, int(count), "", "PHYSICAL_PDF_FAILURE_RECOVERED")
					}
				case 3:
					if scenario == "full-document" {
						data, err := os.ReadFile(path)
						if err != nil || string(data) != "ACTUAL_PDF_FULL_READ_AUTHORIZED_WRITE" {
							t.Error("successful full Read did not authorize actual Write", err, string(data))
						}
					} else if scenario == "page-range" {
						data, err := os.ReadFile(path)
						if err != nil || !bytes.Equal(data, original) || !bytes.Contains(body, []byte("has not been read yet")) {
							t.Error("page range incorrectly authorized full replacement", err, string(body))
						}
					} else {
						t.Error("failed Read retried or ran unintended tool")
					}
					emitPlanFixture(w, int(count), "", "ACTUAL_PDF_PRODUCER_CONSUMER_OK")
				default:
					t.Error("extra PDF model request", count)
					http.Error(w, "extra", 500)
				}
			}))
			defer peer.Close()
			m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["PATH"] = popplerBin + ":/opt/homebrew/bin:/usr/bin:/bin" }, func(options *MainPrintOptions) {
				options.Native.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
			})
			path = filepath.Join(m.options.Native.CWD, "actual-document.pdf")
			pages := 3
			if scenario == "too-many-pages" {
				pages = 11
			}
			original = writePhysicalPDFFixture(t, path, pages)
			if scenario == "invalid-header" {
				original = []byte("<html>not a PDF</html>")
				if err := os.WriteFile(path, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := m.RunTurn(ctx, webObject(`{"type":"user","message":{"role":"user","content":"Read this actual private PDF and complete the requested task."}}`)); err != nil {
				t.Fatal(err)
			}
			expected := int32(2)
			if scenario == "full-document" || scenario == "page-range" {
				expected = 3
			}
			if calls.Load() != expected {
				t.Fatal("actual request cardinality", calls.Load(), expected)
			}
			if err := m.ShutdownSession(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestActualNativeMCPPDFReadBareAndOrdinaryFactories(t *testing.T) {
	popplerBin := requirePhysicalPoppler(t)
	for _, bare := range []bool{true, false} {
		t.Run(fmt.Sprint("bare=", bare), func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
			f.n.Environment["PATH"] = popplerBin + ":/opt/homebrew/bin:/usr/bin:/bin"
			f.n.Environment["ANTHROPIC_API_KEY"] = "fake-no-model"
			f.n.Environment["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:1"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			f.n.NativeProcess = true
			f.n.ImageLibraryPath, _ = filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
			args := []string{"mcp", "serve"}
			if bare {
				args = append([]string{"--bare"}, args...)
			}
			command, err := NewMainCommand(MainCommandOptions{Arguments: args, Native: f.n, Platform: "darwin", Version: "fixture", PID: os.Getpid(), NonInteractive: true, Output: io.Discard, Error: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			defer command.Close()
			host, err := NewNativeMCPToolContext(command, "darwin", false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close(context.Background())
			path := filepath.Join(f.cwd, "mcp-physical.pdf")
			writePhysicalPDFFixture(t, path, 3)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			for _, input := range []map[string]any{{"file_path": path}, {"file_path": path, "pages": "2"}} {
				mapped, err := host.Call(ctx, "Read", rawMain(input))
				if err != nil {
					t.Fatal("actual MCP PDF call", err)
				}
				var blocks []protocol.JSONObject
				if json.Unmarshal(mapped.Get("content"), &blocks) != nil || len(blocks) != 1 || blocks[0].String("type") != "text" {
					t.Fatal("MCP facade did not preserve Source data-only mapper", string(rawMain(mapped)))
				}
				var returned protocol.JSONObject
				if err := returned.UnmarshalJSON([]byte(blocks[0].String("text"))); err != nil {
					t.Fatal("Source MCP data JSON", err)
				}
				expected := "pdf"
				if input["pages"] != nil {
					expected = "parts"
				}
				file, _ := returned.Object("file")
				if returned.String("type") != expected || file.String("filePath") != path {
					t.Fatal("MCP original data-only facade", string(rawMain(mapped)))
				}
				if expected == "parts" && string(file.Get("count")) != "1" {
					t.Fatal("MCP page read was deduplicated or lost pages", string(rawMain(returned)))
				}
			}
			if err := command.Run(ctx, func(scope *cooperative.Scope) error {
				state, present, err := host.cache.Get(scope, path)
				if err != nil {
					return err
				}
				if !present || state.Offset == nil || *state.Offset != 1 || string(state.Content) != `""` {
					return fmt.Errorf("full PDF cache is missing or stored base64: %v", state)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativePDFBindingsRejectForeignOwnerBeforeFileOrProcessAdmission(t *testing.T) {
	m := backgroundAgentMain(t, "http://127.0.0.1:1")
	foreign := cooperative.New()
	defer foreign.Close()
	path := filepath.Join(m.options.Native.CWD, "must-not-read.pdf")
	before := len(m.options.Native.Trace)
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		future, err := m.startNativePDFRead(context.Background(), scope, path, nil)
		if future != nil || !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign PDF producer admitted: %v", err)
		}
		message, err := m.nativePDFMetadataMessage(scope, json.RawMessage(`{"type":"parts","file":{"outputDir":"/unowned"}}`))
		if message != nil || !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign PDF metadata admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(m.options.Native.Trace) != before || m.nativePDFReader != nil {
		t.Fatal("foreign PDF scope created native service or read file")
	}
}

func TestAdmittedNativePDFIgnoresOrdinaryInterruptButRuntimeCloseCancelsAndJoins(t *testing.T) {
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", nil, nil)
	home := m.options.Native.CWD
	bin := filepath.Join(home, "owned-poppler")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "owned-slow.pdf")
	original := writePhysicalPDFFixture(t, path, 1)
	start, release := filepath.Join(home, "pdf-started"), filepath.Join(home, "pdf-release")
	script := "#!/bin/sh\nprintf 'STARTED' > '" + start + "'; while [ ! -f '" + release + "' ]; do /bin/sleep 0.01; done; printf 'Pages: 1\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "pdfinfo"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// This environment is the actual native process observation sampled before
	// the admitted call. No synthetic future or completed PDF is injected.
	if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return m.environment.SetProperty(quoteMain("PATH"), quoteMain(bin+":/usr/bin:/bin"))
	}); err != nil {
		t.Fatal(err)
	}
	for _, closing := range []bool{false, true} {
		if err := os.Remove(start); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.Remove(release); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		type receipt struct {
			data json.RawMessage
			err  error
		}
		done := make(chan receipt, 1)
		go func() {
			var data json.RawMessage
			err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				future, err := m.startNativePDFRead(ctx, scope, path, nil)
				if err != nil {
					return err
				}
				data, err = future.Await(scope)
				return err
			})
			done <- receipt{data, err}
		}()
		deadline := time.After(3 * time.Second)
		timer := time.NewTicker(5 * time.Millisecond)
		waiting := true
		for waiting {
			select {
			case <-timer.C:
				if _, err := os.Stat(start); err == nil {
					waiting = false
				}
			case <-deadline:
				t.Fatal("actual PDF native process did not start")
			}
		}
		timer.Stop()
		cancel()
		if closing {
			m.BeginNativePDFRuntimeClose()
		} else {
			select {
			case got := <-done:
				t.Fatal("ordinary interrupt terminated admitted original PDF", got.err)
			case <-time.After(40 * time.Millisecond):
			}
			if err := os.WriteFile(release, []byte("finish original admitted work"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case got := <-done:
			if closing {
				if !errors.Is(got.err, context.Canceled) {
					t.Fatal("runtime close did not cancel native work", got.err)
				}
			} else {
				if got.err != nil {
					t.Fatal("original PDF did not finish after ordinary interrupt", got.err)
				}
				var value protocol.JSONObject
				_ = value.UnmarshalJSON(got.data)
				file, _ := value.Object("file")
				decoded, err := base64.StdEncoding.DecodeString(file.String("base64"))
				if err != nil || !bytes.Equal(decoded, original) {
					t.Fatal("ordinary interrupt lost actual completed PDF", err)
				}
				if err := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					state, present, err := m.nativeFileCache.Get(scope, path)
					if err != nil {
						return err
					}
					if !present || state.Offset == nil || *state.Offset != 1 {
						return fmt.Errorf("admitted PDF lost cache after interrupt")
					}
					message, err := m.nativePDFMetadataMessage(scope, got.data)
					if err != nil || message == nil {
						return fmt.Errorf("admitted PDF lost metadata after interrupt: %v", err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatal("actual PDF runtime did not join")
		}
	}
	if err := m.ShutdownSession(context.Background()); err != nil {
		t.Fatal(err)
	}
}
