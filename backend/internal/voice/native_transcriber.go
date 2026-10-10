package voice

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/ebitengine/purego"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"
)

type nativeAPI struct {
	libraries         []uintptr
	version           func() string
	createRecognizer  func(*SherpaOnnxOfflineRecognizerConfig) uintptr
	destroyRecognizer func(uintptr)
	setConfig         func(uintptr, *SherpaOnnxOfflineRecognizerConfig)
	createStream      func(uintptr) uintptr
	destroyStream     func(uintptr)
	waveform          func(uintptr, int32, *float32, int32)
	decode            func(uintptr, uintptr)
	resultJSON        func(uintptr) unsafe.Pointer
	destroyJSON       func(unsafe.Pointer)
	createVAD         func(*SherpaOnnxVadModelConfig, float32) uintptr
	destroyVAD        func(uintptr)
	acceptVAD         func(uintptr, *float32, int32)
	emptyVAD          func(uintptr) int32
	frontVAD          func(uintptr) unsafe.Pointer
	destroySegment    func(unsafe.Pointer)
	popVAD            func(uintptr)
	resetVAD          func(uintptr)
	flushVAD          func(uintptr)
}

func openNativeAPI(root string) (*nativeAPI, error) {
	platform := RuntimePlatform(runtime.GOOS, runtime.GOARCH)
	if platform == "" {
		return nil, fmt.Errorf("Local speech recognition is not available on %s-%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := filepath.Join(root, "node_modules", "sherpa-onnx-"+platform)
	onnx, sherpa := "libonnxruntime.so", "libsherpa-onnx-c-api.so"
	if runtime.GOOS == "darwin" {
		onnx, sherpa = "libonnxruntime.dylib", "libsherpa-onnx-c-api.dylib"
	}
	if runtime.GOOS == "windows" {
		onnx, sherpa = "onnxruntime.dll", "sherpa-onnx-c-api.dll"
	}
	api := &nativeAPI{}
	for _, name := range []string{onnx, sherpa} {
		handle, e := openNativeLibrary(filepath.Join(dir, name))
		if e != nil {
			api.close()
			return nil, e
		}
		api.libraries = append(api.libraries, handle)
	}
	handle := api.libraries[len(api.libraries)-1]
	for _, entry := range []struct {
		Name     string
		Function any
	}{{"SherpaOnnxGetVersionStr", &api.version}, {"SherpaOnnxCreateOfflineRecognizer", &api.createRecognizer}, {"SherpaOnnxDestroyOfflineRecognizer", &api.destroyRecognizer}, {"SherpaOnnxOfflineRecognizerSetConfig", &api.setConfig}, {"SherpaOnnxCreateOfflineStream", &api.createStream}, {"SherpaOnnxDestroyOfflineStream", &api.destroyStream}, {"SherpaOnnxAcceptWaveformOffline", &api.waveform}, {"SherpaOnnxDecodeOfflineStream", &api.decode}, {"SherpaOnnxGetOfflineStreamResultAsJson", &api.resultJSON}, {"SherpaOnnxDestroyOfflineStreamResultJson", &api.destroyJSON}, {"SherpaOnnxCreateVoiceActivityDetector", &api.createVAD}, {"SherpaOnnxDestroyVoiceActivityDetector", &api.destroyVAD}, {"SherpaOnnxVoiceActivityDetectorAcceptWaveform", &api.acceptVAD}, {"SherpaOnnxVoiceActivityDetectorEmpty", &api.emptyVAD}, {"SherpaOnnxVoiceActivityDetectorFront", &api.frontVAD}, {"SherpaOnnxDestroySpeechSegment", &api.destroySegment}, {"SherpaOnnxVoiceActivityDetectorPop", &api.popVAD}, {"SherpaOnnxVoiceActivityDetectorReset", &api.resetVAD}, {"SherpaOnnxVoiceActivityDetectorFlush", &api.flushVAD}} {
		symbol, e := lookupNativeSymbol(handle, entry.Name)
		if e != nil {
			api.close()
			return nil, e
		}
		purego.RegisterFunc(entry.Function, symbol)
	}
	if version := strings.TrimPrefix(api.version(), "v"); version != SherpaVersion {
		api.close()
		return nil, fmt.Errorf("Speech runtime version %s does not match %s", version, SherpaVersion)
	}
	return api, nil
}
func (api *nativeAPI) close() {
	for i := len(api.libraries) - 1; i >= 0; i-- {
		_ = closeNativeLibrary(api.libraries[i])
	}
	api.libraries = nil
}

type NativeTranscriber struct {
	mu              sync.Mutex
	api             *nativeAPI
	recognizer, vad uintptr
	config          SherpaOnnxOfflineRecognizerConfig
	strings         [][]byte
	closed          bool
}

func (t *NativeTranscriber) cstr(value string) uintptr {
	data := append([]byte(value), 0)
	t.strings = append(t.strings, data)
	return uintptr(unsafe.Pointer(&data[0]))
}
func NewNativeTranscriber(config WorkerConfig) (*NativeTranscriber, error) {
	api, e := openNativeAPI(config.RuntimeRoot)
	if e != nil {
		return nil, e
	}
	t := &NativeTranscriber{api: api}
	t.config.Feat_config = SherpaOnnxFeatureConfig{Sample_rate: 16000, Feature_dim: 80}
	t.config.Model_config.Sense_voice = SherpaOnnxOfflineSenseVoiceModelConfig{Model: t.cstr(config.Model), Language: t.cstr("auto"), Use_itn: 1}
	t.config.Model_config.Tokens = t.cstr(config.Tokens)
	t.config.Model_config.Num_threads = int32(config.Threads)
	t.config.Model_config.Provider = t.cstr("cpu")
	t.config.Decoding_method = t.cstr("greedy_search")
	t.recognizer = api.createRecognizer(&t.config)
	runtime.KeepAlive(t.strings)
	if t.recognizer == 0 {
		api.close()
		return nil, fmt.Errorf("Could not create SenseVoice recognizer")
	}
	vad := SherpaOnnxVadModelConfig{Silero_vad: SherpaOnnxSileroVadModelConfig{Model: t.cstr(config.VAD), Threshold: float32(config.VADThreshold), Min_silence_duration: float32(config.MinSilenceSeconds), Min_speech_duration: float32(config.MinSpeechSeconds), Max_speech_duration: float32(config.SegmentSeconds), Window_size: 512}, Sample_rate: 16000, Num_threads: int32(config.Threads), Provider: t.cstr("cpu")}
	t.vad = api.createVAD(&vad, float32(config.SegmentSeconds+config.MinSilenceSeconds+1))
	runtime.KeepAlive(t.strings)
	if t.vad == 0 {
		api.destroyRecognizer(t.recognizer)
		api.close()
		return nil, fmt.Errorf("Could not create Silero voice activity detector")
	}
	return t, nil
}
func (t *NativeTranscriber) Transcribe(wav []byte, language string) (Transcript, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return Transcript{}, fmt.Errorf("Speech recognizer disposed")
	}
	if !Language(language) {
		return Transcript{}, &workerInputError{"Unsupported SenseVoice language"}
	}
	samples, e := ReadSamples(wav)
	if e != nil {
		return Transcript{}, &workerInputError{e.Error()}
	}
	t.config.Model_config.Sense_voice.Language = t.cstr(language)
	t.api.setConfig(t.recognizer, &t.config)
	runtime.KeepAlive(t.strings)
	t.api.resetVAD(t.vad)
	started := time.Now()
	texts := []string{}
	drain := func() error {
		for t.api.emptyVAD(t.vad) == 0 {
			pointer := t.api.frontVAD(t.vad)
			if pointer == nil {
				return fmt.Errorf("Speech detector returned an invalid segment")
			}
			segment := (*SherpaOnnxSpeechSegment)(pointer)
			if segment.N < 0 || segment.N > 16000*121 || segment.Samples == nil {
				t.api.destroySegment(pointer)
				return fmt.Errorf("Speech detector returned invalid sample data")
			}
			native := unsafe.Slice((*float32)(segment.Samples), int(segment.N))
			copySamples := append([]float32(nil), native...)
			stream := t.api.createStream(t.recognizer)
			if stream == 0 {
				t.api.destroySegment(pointer)
				return fmt.Errorf("Could not create SenseVoice stream")
			}
			if len(copySamples) > 0 {
				t.api.waveform(stream, 16000, &copySamples[0], int32(len(copySamples)))
			}
			t.api.decode(t.recognizer, stream)
			result := t.api.resultJSON(stream)
			if result == nil {
				t.api.destroyStream(stream)
				t.api.destroySegment(pointer)
				return fmt.Errorf("SenseVoice returned no transcription")
			}
			data := nativeCString(result)
			text := protocol.TrimJavaScriptString(object([]byte(data)).String("text"))
			t.api.destroyJSON(result)
			t.api.destroyStream(stream)
			t.api.destroySegment(pointer)
			t.api.popVAD(t.vad)
			if text != "" {
				texts = append(texts, text)
			}
			runtime.KeepAlive(copySamples)
		}
		return nil
	}
	for offset := 0; offset < len(samples); offset += 512 {
		window := samples[offset:min(offset+512, len(samples))]
		t.api.acceptVAD(t.vad, &window[0], int32(len(window)))
		if e = drain(); e != nil {
			return Transcript{}, e
		}
	}
	t.api.flushVAD(t.vad)
	if e = drain(); e != nil {
		return Transcript{}, e
	}
	runtime.KeepAlive(samples)
	return Transcript{strings.TrimSpace(strings.Join(texts, " ")), float64(len(samples)) / 16000, time.Since(started).Seconds()}, nil
}
func nativeCString(pointer unsafe.Pointer) string {
	data := []byte{}
	for i := 0; i < 1024*1024; i++ {
		value := *(*byte)(unsafe.Add(pointer, i))
		if value == 0 {
			return string(data)
		}
		data = append(data, value)
	}
	return string(data)
}
func (t *NativeTranscriber) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true
	t.api.destroyVAD(t.vad)
	t.api.destroyRecognizer(t.recognizer)
	t.api.close()
	t.strings = nil
}
