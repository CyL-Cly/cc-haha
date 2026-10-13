import {
  DEEPSEEK_ANTHROPIC_BASE_URL,
  DEEPSEEK_AUTH_HEADER,
  DEEPSEEK_OAUTH_DUMMY_KEY,
} from '../../services/deepseekAuth/client.js'
import {
  DEEPSEEK_OAUTH_FILE_ENV_KEY,
  isInferenceEligibleGrant,
} from '../../services/deepseekAuth/storage.js'
import { PROVIDER_PRESETS } from '../config/providerPresets.js'
import {
  DEEPSEEK_ACCOUNT_PROVIDER_ID,
  type SavedProvider,
} from '../types/provider.js'
import {
  getHahaDeepSeekOAuthFilePath,
  hahaDeepSeekOAuthService,
} from './hahaDeepSeekOAuthService.js'

export { DEEPSEEK_ACCOUNT_PROVIDER_ID, DEEPSEEK_OAUTH_FILE_ENV_KEY }
export const DEEPSEEK_ACCOUNT_PROVIDER_NAME = 'DeepSeek Account'
export const DEEPSEEK_OAUTH_PROVIDER_ENV_KEY = 'CC_HAHA_DEEPSEEK_OAUTH_PROVIDER'

export function isDeepSeekAccountProviderId(id: string | null | undefined): boolean {
  return id === DEEPSEEK_ACCOUNT_PROVIDER_ID
}

// The account route serves the same models as the API route, so the bundled
// `deepseek` preset stays the single source of the default mapping and
// context windows instead of a second copy that could drift.
const deepseekPreset = PROVIDER_PRESETS.find((preset) => preset.id === 'deepseek')
if (!deepseekPreset) {
  throw new Error('DeepSeek account provider requires the bundled deepseek preset')
}

export const DEEPSEEK_ACCOUNT_PROVIDER: SavedProvider = {
  id: DEEPSEEK_ACCOUNT_PROVIDER_ID,
  presetId: deepseekPreset.id,
  name: DEEPSEEK_ACCOUNT_PROVIDER_NAME,
  apiKey: '',
  authStrategy: 'api_key',
  baseUrl: DEEPSEEK_ANTHROPIC_BASE_URL,
  apiFormat: 'anthropic',
  runtimeKind: 'deepseek_oauth',
  models: { ...deepseekPreset.defaultModels },
  ...(deepseekPreset.modelContextWindows && {
    modelContextWindows: { ...deepseekPreset.modelContextWindows },
  }),
}

/**
 * Layer the account markers over the env an Anthropic-compatible DeepSeek
 * provider would get. The CLI keeps the normal request shape (model mapping,
 * capability flags, context windows) and only its credential changes: a dummy
 * API key that `buildDeepSeekFetch` replaces with the stored account token on
 * each request. No bearer token is exported, so a logged-out account can never
 * fall through to another credential.
 */
export function applyDeepSeekAccountRuntimeEnv(
  baseEnv: Record<string, string>,
): Record<string, string> {
  const { ANTHROPIC_AUTH_TOKEN: _authToken, ...env } = baseEnv
  return {
    ...env,
    ANTHROPIC_API_KEY: DEEPSEEK_OAUTH_DUMMY_KEY,
    [DEEPSEEK_OAUTH_PROVIDER_ENV_KEY]: '1',
    [DEEPSEEK_OAUTH_FILE_ENV_KEY]: getHahaDeepSeekOAuthFilePath(),
  }
}

/**
 * Headers for the server's own small Anthropic-format calls (session titles,
 * auto-answered questions) made with the account grant. Null while signed out,
 * so callers skip the call instead of sending an unauthenticated request.
 */
export async function resolveDeepSeekAccountRequestHeaders(): Promise<
  Record<string, string> | null
> {
  const tokens = await hahaDeepSeekOAuthService.loadTokens()
  if (!tokens || !isInferenceEligibleGrant(tokens)) return null
  return {
    'Content-Type': 'application/json',
    'anthropic-version': '2023-06-01',
    [DEEPSEEK_AUTH_HEADER]: tokens.token,
  }
}
