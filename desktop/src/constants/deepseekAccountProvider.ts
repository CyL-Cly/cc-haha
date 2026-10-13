import { BUNDLED_PROVIDER_PRESETS } from '../config/providerPresets'
import type { ModelMapping, SavedProvider } from '../types/provider'

export const DEEPSEEK_ACCOUNT_PROVIDER_ID = 'deepseek-account'
export const DEEPSEEK_ACCOUNT_PROVIDER_NAME = 'DeepSeek Account'

// The account provider talks to the same DeepSeek models as the API-key
// provider, so the bundled `deepseek` preset stays the single source of truth
// for the slot mapping and the endpoint shape.
const DEEPSEEK_PRESET_ID = 'deepseek'
const deepSeekPreset = BUNDLED_PROVIDER_PRESETS.find((preset) => preset.id === DEEPSEEK_PRESET_ID)

export const DEEPSEEK_ACCOUNT_MODEL_MAPPING: ModelMapping = {
  main: deepSeekPreset?.defaultModels.main ?? '',
  haiku: deepSeekPreset?.defaultModels.haiku ?? '',
  sonnet: deepSeekPreset?.defaultModels.sonnet ?? '',
  opus: deepSeekPreset?.defaultModels.opus ?? '',
  ...(deepSeekPreset?.defaultModels.fable ? { fable: deepSeekPreset.defaultModels.fable } : {}),
}

export const DEEPSEEK_ACCOUNT_DEFAULT_MODEL_ID = DEEPSEEK_ACCOUNT_MODEL_MAPPING.main

/**
 * A saved-provider-shaped view of the built-in account provider. The model
 * picker builds its rows (slot labels, reasoning options) from saved providers,
 * and an account login has to look the same there as the API-key provider.
 */
export function buildDeepSeekAccountProvider(): SavedProvider {
  return {
    id: DEEPSEEK_ACCOUNT_PROVIDER_ID,
    presetId: DEEPSEEK_PRESET_ID,
    name: DEEPSEEK_ACCOUNT_PROVIDER_NAME,
    apiKey: '',
    baseUrl: deepSeekPreset?.baseUrl ?? '',
    apiFormat: 'anthropic',
    runtimeKind: 'deepseek_oauth',
    models: { ...DEEPSEEK_ACCOUNT_MODEL_MAPPING },
  }
}
