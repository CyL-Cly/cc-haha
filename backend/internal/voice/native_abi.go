package voice

import "unsafe"

// Native C ABI pinned to sherpa-onnx v1.13.8, the same library shipped in the
// original verified npm packages. Source: https://github.com/k2-fsa/sherpa-onnx/blob/v1.13.8/sherpa-onnx/c-api/c-api.h
// Copyright 2023 Xiaomi Corporation. Apache-2.0.

type SherpaOnnxFeatureConfig struct {
	Sample_rate int32
	Feature_dim int32
}

type SherpaOnnxOfflineTransducerModelConfig struct {
	Encoder uintptr
	Decoder uintptr
	Joiner  uintptr
}

type SherpaOnnxOfflineParaformerModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineNemoEncDecCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineWhisperModelConfig struct {
	Encoder                   uintptr
	Decoder                   uintptr
	Language                  uintptr
	Task                      uintptr
	Tail_paddings             int32
	Enable_token_timestamps   int32
	Enable_segment_timestamps int32
}

type SherpaOnnxOfflineTdnnModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineSenseVoiceModelConfig struct {
	Model    uintptr
	Language uintptr
	Use_itn  int32
}

type SherpaOnnxOfflineMoonshineModelConfig struct {
	Preprocessor     uintptr
	Encoder          uintptr
	Uncached_decoder uintptr
	Cached_decoder   uintptr
	Merged_decoder   uintptr
}

type SherpaOnnxOfflineFireRedAsrModelConfig struct {
	Encoder uintptr
	Decoder uintptr
}

type SherpaOnnxOfflineDolphinModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineZipformerCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineCanaryModelConfig struct {
	Encoder  uintptr
	Decoder  uintptr
	Src_lang uintptr
	Tgt_lang uintptr
	Use_pnc  int32
}

type SherpaOnnxOfflineWenetCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineOmnilingualAsrCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineMedAsrCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineFunASRNanoModelConfig struct {
	Encoder_adaptor uintptr
	Llm             uintptr
	Embedding       uintptr
	Tokenizer       uintptr
	System_prompt   uintptr
	User_prompt     uintptr
	Max_new_tokens  int32
	Temperature     float32
	Top_p           float32
	Seed            int32
	Language        uintptr
	Itn             int32
	Hotwords        uintptr
}

type SherpaOnnxOfflineFireRedAsrCtcModelConfig struct {
	Model uintptr
}

type SherpaOnnxOfflineQwen3ASRModelConfig struct {
	Conv_frontend  uintptr
	Encoder        uintptr
	Decoder        uintptr
	Tokenizer      uintptr
	Max_total_len  int32
	Max_new_tokens int32
	Temperature    float32
	Top_p          float32
	Seed           int32
	Hotwords       uintptr
}

type SherpaOnnxOfflineCohereTranscribeModelConfig struct {
	Encoder   uintptr
	Decoder   uintptr
	Language  uintptr
	Use_punct int32
	Use_itn   int32
}

type SherpaOnnxOfflineModelConfig struct {
	Transducer        SherpaOnnxOfflineTransducerModelConfig
	Paraformer        SherpaOnnxOfflineParaformerModelConfig
	Nemo_ctc          SherpaOnnxOfflineNemoEncDecCtcModelConfig
	Whisper           SherpaOnnxOfflineWhisperModelConfig
	Tdnn              SherpaOnnxOfflineTdnnModelConfig
	Tokens            uintptr
	Num_threads       int32
	Debug             int32
	Provider          uintptr
	Model_type        uintptr
	Modeling_unit     uintptr
	Bpe_vocab         uintptr
	Telespeech_ctc    uintptr
	Sense_voice       SherpaOnnxOfflineSenseVoiceModelConfig
	Moonshine         SherpaOnnxOfflineMoonshineModelConfig
	Fire_red_asr      SherpaOnnxOfflineFireRedAsrModelConfig
	Dolphin           SherpaOnnxOfflineDolphinModelConfig
	Zipformer_ctc     SherpaOnnxOfflineZipformerCtcModelConfig
	Canary            SherpaOnnxOfflineCanaryModelConfig
	Wenet_ctc         SherpaOnnxOfflineWenetCtcModelConfig
	Omnilingual       SherpaOnnxOfflineOmnilingualAsrCtcModelConfig
	Medasr            SherpaOnnxOfflineMedAsrCtcModelConfig
	Funasr_nano       SherpaOnnxOfflineFunASRNanoModelConfig
	Fire_red_asr_ctc  SherpaOnnxOfflineFireRedAsrCtcModelConfig
	Qwen3_asr         SherpaOnnxOfflineQwen3ASRModelConfig
	Cohere_transcribe SherpaOnnxOfflineCohereTranscribeModelConfig
}

type SherpaOnnxOfflineLMConfig struct {
	Model uintptr
	Scale float32
}

type SherpaOnnxHomophoneReplacerConfig struct {
	Dict_dir  uintptr
	Lexicon   uintptr
	Rule_fsts uintptr
}

type SherpaOnnxOfflineRecognizerConfig struct {
	Feat_config      SherpaOnnxFeatureConfig
	Model_config     SherpaOnnxOfflineModelConfig
	Lm_config        SherpaOnnxOfflineLMConfig
	Decoding_method  uintptr
	Max_active_paths int32
	Hotwords_file    uintptr
	Hotwords_score   float32
	Rule_fsts        uintptr
	Rule_fars        uintptr
	Blank_penalty    float32
	Hr               SherpaOnnxHomophoneReplacerConfig
}

type SherpaOnnxSileroVadModelConfig struct {
	Model                uintptr
	Threshold            float32
	Min_silence_duration float32
	Min_speech_duration  float32
	Window_size          int32
	Max_speech_duration  float32
}

type SherpaOnnxTenVadModelConfig struct {
	Model                uintptr
	Threshold            float32
	Min_silence_duration float32
	Min_speech_duration  float32
	Window_size          int32
	Max_speech_duration  float32
}

type SherpaOnnxVadModelConfig struct {
	Silero_vad  SherpaOnnxSileroVadModelConfig
	Sample_rate int32
	Num_threads int32
	Provider    uintptr
	Debug       int32
	Ten_vad     SherpaOnnxTenVadModelConfig
}

type SherpaOnnxSpeechSegment struct {
	Start   int32
	Samples unsafe.Pointer
	N       int32
}
