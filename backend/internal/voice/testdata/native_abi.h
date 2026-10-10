// sherpa-onnx v1.13.8 C ABI declarations used by the native parity check.
// Copyright 2023 Xiaomi Corporation. Apache-2.0.
#include <stdint.h>
typedef struct SherpaOnnxFeatureConfig { 
  
  int32_t sample_rate;

  
  int32_t feature_dim;
 } SherpaOnnxFeatureConfig;
typedef struct SherpaOnnxOfflineTransducerModelConfig { 
  
  const char *encoder;
  
  const char *decoder;
  
  const char *joiner;
 } SherpaOnnxOfflineTransducerModelConfig;
typedef struct SherpaOnnxOfflineParaformerModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineParaformerModelConfig;
typedef struct SherpaOnnxOfflineNemoEncDecCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineNemoEncDecCtcModelConfig;
typedef struct SherpaOnnxOfflineWhisperModelConfig { 
  
  const char *encoder;
  
  const char *decoder;
  
  const char *language;
  
  const char *task;
  
  int32_t tail_paddings;

  
  int32_t enable_token_timestamps;

  
  int32_t enable_segment_timestamps;
 } SherpaOnnxOfflineWhisperModelConfig;
typedef struct SherpaOnnxOfflineTdnnModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineTdnnModelConfig;
typedef struct SherpaOnnxOfflineSenseVoiceModelConfig { 
  
  const char *model;
  
  const char *language;
  
  int32_t use_itn;
 } SherpaOnnxOfflineSenseVoiceModelConfig;
typedef struct SherpaOnnxOfflineMoonshineModelConfig { 
  
  const char *preprocessor;
  
  const char *encoder;
  
  const char *uncached_decoder;
  
  const char *cached_decoder;
  
  const char *merged_decoder;
 } SherpaOnnxOfflineMoonshineModelConfig;
typedef struct SherpaOnnxOfflineFireRedAsrModelConfig { 
  
  const char *encoder;
  
  const char *decoder;
 } SherpaOnnxOfflineFireRedAsrModelConfig;
typedef struct SherpaOnnxOfflineDolphinModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineDolphinModelConfig;
typedef struct SherpaOnnxOfflineZipformerCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineZipformerCtcModelConfig;
typedef struct SherpaOnnxOfflineCanaryModelConfig { 
  
  const char *encoder;
  
  const char *decoder;
  
  const char *src_lang;
  
  const char *tgt_lang;
  
  int32_t use_pnc;
 } SherpaOnnxOfflineCanaryModelConfig;
typedef struct SherpaOnnxOfflineWenetCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineWenetCtcModelConfig;
typedef struct SherpaOnnxOfflineOmnilingualAsrCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineOmnilingualAsrCtcModelConfig;
typedef struct SherpaOnnxOfflineMedAsrCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineMedAsrCtcModelConfig;
typedef struct SherpaOnnxOfflineFunASRNanoModelConfig { 
  
  const char *encoder_adaptor;
  
  const char *llm;
  
  const char *embedding;
  
  const char *tokenizer;
  
  const char *system_prompt;
  
  const char *user_prompt;
  
  int32_t max_new_tokens;
  
  float temperature;
  
  float top_p;
  
  int32_t seed;
  
  const char *language;
  
  int32_t itn;
  
  const char *hotwords;
 } SherpaOnnxOfflineFunASRNanoModelConfig;
typedef struct SherpaOnnxOfflineFireRedAsrCtcModelConfig { 
  
  const char *model;
 } SherpaOnnxOfflineFireRedAsrCtcModelConfig;
typedef struct SherpaOnnxOfflineQwen3ASRModelConfig { 
  
  const char *conv_frontend;
  
  const char *encoder;
  
  const char *decoder;
  
  const char *tokenizer;
  
  int32_t max_total_len;
  
  int32_t max_new_tokens;
  
  float temperature;
  
  float top_p;
  
  int32_t seed;
  
  const char *hotwords;
 } SherpaOnnxOfflineQwen3ASRModelConfig;
typedef struct SherpaOnnxOfflineCohereTranscribeModelConfig { 
  
  const char *encoder;
  
  const char *decoder;
  
  const char *language;
  
  int32_t use_punct;
  
  int32_t use_itn;
 } SherpaOnnxOfflineCohereTranscribeModelConfig;
typedef struct SherpaOnnxOfflineModelConfig { 
  
  SherpaOnnxOfflineTransducerModelConfig transducer;
  
  SherpaOnnxOfflineParaformerModelConfig paraformer;
  
  SherpaOnnxOfflineNemoEncDecCtcModelConfig nemo_ctc;
  
  SherpaOnnxOfflineWhisperModelConfig whisper;
  
  SherpaOnnxOfflineTdnnModelConfig tdnn;

  
  const char *tokens;
  
  int32_t num_threads;
  
  int32_t debug;
  
  const char *provider;
  
  const char *model_type;
  
  const char *modeling_unit;
  
  const char *bpe_vocab;
  
  const char *telespeech_ctc;
  
  SherpaOnnxOfflineSenseVoiceModelConfig sense_voice;
  
  SherpaOnnxOfflineMoonshineModelConfig moonshine;
  
  SherpaOnnxOfflineFireRedAsrModelConfig fire_red_asr;
  
  SherpaOnnxOfflineDolphinModelConfig dolphin;
  
  SherpaOnnxOfflineZipformerCtcModelConfig zipformer_ctc;
  
  SherpaOnnxOfflineCanaryModelConfig canary;
  
  SherpaOnnxOfflineWenetCtcModelConfig wenet_ctc;
  
  SherpaOnnxOfflineOmnilingualAsrCtcModelConfig omnilingual;
  
  SherpaOnnxOfflineMedAsrCtcModelConfig medasr;
  
  SherpaOnnxOfflineFunASRNanoModelConfig funasr_nano;
  
  SherpaOnnxOfflineFireRedAsrCtcModelConfig fire_red_asr_ctc;
  
  SherpaOnnxOfflineQwen3ASRModelConfig qwen3_asr;
  
  SherpaOnnxOfflineCohereTranscribeModelConfig cohere_transcribe;
 } SherpaOnnxOfflineModelConfig;
typedef struct SherpaOnnxOfflineLMConfig { 
  
  const char *model;
  
  float scale;
 } SherpaOnnxOfflineLMConfig;
typedef struct SherpaOnnxHomophoneReplacerConfig { 
  
  const char *dict_dir;
  
  const char *lexicon;
  
  const char *rule_fsts;
 } SherpaOnnxHomophoneReplacerConfig;
typedef struct SherpaOnnxOfflineRecognizerConfig { 
  
  SherpaOnnxFeatureConfig feat_config;
  
  SherpaOnnxOfflineModelConfig model_config;
  
  SherpaOnnxOfflineLMConfig lm_config;

  
  const char *decoding_method;
  
  int32_t max_active_paths;

  
  const char *hotwords_file;

  
  float hotwords_score;
  
  const char *rule_fsts;
  
  const char *rule_fars;
  
  float blank_penalty;

  
  SherpaOnnxHomophoneReplacerConfig hr;
 } SherpaOnnxOfflineRecognizerConfig;
typedef struct SherpaOnnxSileroVadModelConfig { 
  
  const char *model;
  
  float threshold;
  
  float min_silence_duration;
  
  float min_speech_duration;
  
  int32_t window_size;
  
  float max_speech_duration;
 } SherpaOnnxSileroVadModelConfig;
typedef struct SherpaOnnxTenVadModelConfig { 
  
  const char *model;
  
  float threshold;
  
  float min_silence_duration;
  
  float min_speech_duration;
  
  int32_t window_size;
  
  float max_speech_duration;
 } SherpaOnnxTenVadModelConfig;
typedef struct SherpaOnnxVadModelConfig { 
  
  SherpaOnnxSileroVadModelConfig silero_vad;
  
  int32_t sample_rate;
  
  int32_t num_threads;
  
  const char *provider;
  
  int32_t debug;
  
  SherpaOnnxTenVadModelConfig ten_vad;
 } SherpaOnnxVadModelConfig;
typedef struct SherpaOnnxSpeechSegment { 
  
  int32_t start;
  
  float *samples;
  
  int32_t n;
 } SherpaOnnxSpeechSegment;
