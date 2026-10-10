package voice

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
)

type SenseOptions struct {
	DataRoot       string
	Environment    map[string]string
	Platform, Arch string
	Items          []Item
	Download       DownloadOptions
	Extract        ExtractArchive
	Recognizer     RecognizerOptions
	Threads        *int
}
type SenseProvider struct {
	o          SenseOptions
	layout     Layout
	items      []Item
	platform   string
	supported  bool
	info       Info
	recognizer *Recognizer
	mu         sync.Mutex
	preparing  bool
}

func NewSenseProvider(o SenseOptions) (*SenseProvider, error) {
	if !filepath.IsAbs(o.DataRoot) {
		return nil, errors.New("voice assets require an explicit private data root")
	}
	if o.Platform == "" {
		o.Platform = runtime.GOOS
		if o.Platform == "windows" {
			o.Platform = "win32"
		}
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
		if o.Arch == "amd64" {
			o.Arch = "x64"
		}
	}
	platform := RuntimePlatform(o.Platform, o.Arch)
	items := o.Items
	if items == nil {
		items = InstallItems(platform, "auto")
	}
	layout := SenseVoiceLayout(o.DataRoot)
	if o.Extract == nil {
		o.Extract = SystemTar(o.Environment)
	}
	threads := 2
	if o.Threads != nil {
		threads = *o.Threads
	}
	recognizerOptions := o.Recognizer
	recognizerOptions.Environment = o.Environment
	recognizerOptions.CWD = layout.Base
	recognizerOptions.Config = func() (WorkerConfig, error) {
		return WorkerConfig{RuntimeRoot: layout.RuntimeRoot, Model: filepath.Join(layout.Models, "model.int8.onnx"), Tokens: filepath.Join(layout.Models, "tokens.txt"), VAD: filepath.Join(layout.Models, "silero_vad.onnx"), Threads: threads, MaxAudioBytes: MaxAudioBytes, VADThreshold: 0.5, MinSilenceSeconds: 0.5, MinSpeechSeconds: 0.25, SegmentSeconds: 30}, nil
	}
	recognizer, e := NewRecognizer(recognizerOptions)
	if e != nil {
		return nil, e
	}
	total := TotalDownloadBytes(items)
	return &SenseProvider{o: o, layout: layout, items: items, platform: platform, supported: o.Items != nil || platform != "", recognizer: recognizer, info: Info{ID: ProviderID, Name: "SenseVoice Small (INT8)", Location: "local", Languages: append([]string(nil), Languages...), DownloadBytes: ptr(total)}}, nil
}
func (p *SenseProvider) Info() Info { return p.info }
func (p *SenseProvider) unsupported() Preparation {
	return Preparation{Phase: "failed", Error: &Failure{Reason: "unsupported-platform", Message: fmt.Sprintf("Local speech recognition is not available on %s-%s", p.o.Platform, p.o.Arch)}}
}
func (p *SenseProvider) Status(context.Context) (Preparation, error) {
	if !p.supported {
		return p.unsupported(), nil
	}
	if IsInstalled(p.layout, p.items) {
		return Preparation{Phase: "ready"}, nil
	}
	completed := BytesOnDisk(p.layout, p.items)
	if completed > 0 {
		return Preparation{Phase: "unprepared", CompletedBytes: ptr(completed), TotalBytes: p.info.DownloadBytes}, nil
	}
	return Preparation{Phase: "unprepared"}, nil
}
func (p *SenseProvider) Prepare(ctx context.Context, source string, report func(Preparation)) error {
	if !p.supported {
		state := p.unsupported()
		report(state)
		return failure("voice/failed", state.Error.Message)
	}
	p.mu.Lock()
	if p.preparing {
		p.mu.Unlock()
		return errors.New("Speech preparation is already running")
	}
	p.preparing = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.preparing = false; p.mu.Unlock() }()
	items := p.o.Items
	if items == nil {
		items = InstallItems(p.platform, source)
	}
	latest := Preparation{Phase: "downloading"}
	e := InstallAll(ctx, InstallOptions{p.layout, items, p.o.Download, p.o.Extract}, func(state Preparation) { latest = state; report(state) })
	if e == nil {
		report(Preparation{Phase: "ready"})
		return nil
	}
	if ctx.Err() != nil {
		report(Preparation{Phase: "cancelled", CompletedBytes: latest.CompletedBytes, TotalBytes: p.info.DownloadBytes})
		return context.Cause(ctx)
	}
	reason := &Failure{Reason: ClassifyError(e), Message: e.Error()}
	var own *DownloadError
	if errors.As(e, &own) {
		copy := own.Failure
		reason = &copy
	}
	report(Preparation{Phase: "failed", Step: latest.Step, Resource: latest.Resource, CompletedBytes: latest.CompletedBytes, TotalBytes: p.info.DownloadBytes, Source: reason.Source, Error: reason})
	return e
}
func (p *SenseProvider) Remove(ctx context.Context) error {
	if e := p.recognizer.StopWorker(ctx); e != nil {
		return e
	}
	return RemoveInstall(p.layout)
}
func (p *SenseProvider) Transcribe(ctx context.Context, wav []byte, language string) (Transcript, error) {
	if !p.supported || !IsInstalled(p.layout, p.items) {
		return Transcript{}, failure("voice/not-ready", "Speech model is not downloaded yet")
	}
	return p.recognizer.Transcribe(ctx, wav, language)
}
func (p *SenseProvider) Close(ctx context.Context) error { return p.recognizer.Close(ctx) }
