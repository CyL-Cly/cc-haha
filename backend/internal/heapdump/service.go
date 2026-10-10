// Package heapdump captures real Go runtime memory evidence. The migrated
// runtime produces a binary pprof heap profile, not a fabricated V8 snapshot.
package heapdump

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

type Diagnostics struct {
	Timestamp      string      `json:"timestamp"`
	SessionID      string      `json:"sessionId"`
	Trigger        string      `json:"trigger"`
	DumpNumber     int         `json:"dumpNumber"`
	Runtime        string      `json:"runtime"`
	RuntimeVersion string      `json:"runtimeVersion"`
	CCVersion      string      `json:"ccVersion"`
	Platform       string      `json:"platform"`
	UptimeSeconds  float64     `json:"uptimeSeconds"`
	MemoryUsage    MemoryUsage `json:"memoryUsage"`
}
type MemoryUsage struct {
	HeapAlloc    uint64 `json:"heapAllocBytes"`
	HeapInuse    uint64 `json:"heapInuseBytes"`
	HeapSys      uint64 `json:"heapSysBytes"`
	StackInuse   uint64 `json:"stackInuseBytes"`
	Sys          uint64 `json:"sysBytes"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
	NumGC        uint32 `json:"numGC"`
	PauseTotalNS uint64 `json:"pauseTotalNanoseconds"`
	Goroutines   int    `json:"goroutines"`
}

type Options struct {
	DesktopPath      string
	SessionID        string
	Version          string
	Now              func() time.Time
	Capture          func() Diagnostics
	Mkdir            func(string) error
	WriteDiagnostics func(string, []byte) error
	WriteHeap        func(string) error
}
type Result struct {
	Success                   bool
	HeapPath, DiagPath, Error string
}

func (r Result) Text() string {
	if !r.Success {
		return "Failed to create heap dump: " + r.Error
	}
	return r.HeapPath + "\n" + r.DiagPath
}

func Capture(start time.Time, now time.Time) Diagnostics {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return Diagnostics{Timestamp: now.UTC().Format("2006-01-02T15:04:05.000Z"), Runtime: "go", RuntimeVersion: runtime.Version(), Platform: runtime.GOOS, UptimeSeconds: now.Sub(start).Seconds(), MemoryUsage: MemoryUsage{stats.HeapAlloc, stats.HeapInuse, stats.HeapSys, stats.StackInuse, stats.Sys, stats.Mallocs, stats.Frees, stats.NumGC, stats.PauseTotalNs, runtime.NumGoroutine()}}
}

func Dump(o Options, trigger string, number int) Result {
	fail := func(err error) Result { return Result{Error: err.Error()} }
	if o.DesktopPath == "" || o.SessionID == "" || o.Now == nil || o.Capture == nil || o.Mkdir == nil || o.WriteDiagnostics == nil || o.WriteHeap == nil {
		return fail(errors.New("heap dump requires actual runtime, filesystem and activated session"))
	}
	// Snapshot precedes allocation/IO for serialization, exactly as the original
	// diagnostic workflow, so the heap dump cannot skew the reported sample.
	diagnostics := o.Capture()
	diagnostics.SessionID = o.SessionID
	diagnostics.Trigger = trigger
	diagnostics.DumpNumber = number
	diagnostics.CCVersion = o.Version
	if err := o.Mkdir(o.DesktopPath); err != nil {
		return fail(err)
	}
	suffix := ""
	if number > 0 {
		suffix = fmt.Sprintf("-dump%d", number)
	}
	heapPath := filepath.Join(o.DesktopPath, o.SessionID+suffix+".heap.pprof")
	diagPath := filepath.Join(o.DesktopPath, o.SessionID+suffix+"-diagnostics.json")
	data, err := json.MarshalIndent(diagnostics, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err = o.WriteDiagnostics(diagPath, data); err != nil {
		return fail(err)
	}
	if err = o.WriteHeap(heapPath); err != nil {
		return fail(err)
	}
	return Result{Success: true, HeapPath: heapPath, DiagPath: diagPath}
}

func WriteProfile(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = WriteProfileTo(file)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		runtime.GC()
	}
	return err
}
func WriteProfileTo(writer io.Writer) error { return pprof.Lookup("heap").WriteTo(writer, 0) }
