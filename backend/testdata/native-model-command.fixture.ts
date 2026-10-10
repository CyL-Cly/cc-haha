import fs from 'node:fs'
import { mock } from 'bun:test'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MODEL_COMMAND_FIXTURE !== '1') throw new Error('Original model command requires a private fixture and pinned Bun')
const home = assertDisposableFixtureHome(input.home, 'typed model command')
if (input.certificate) {
  fs.writeFileSync(join(home, 'model-validation-root.pem'), input.certificate)
  process.env.NODE_EXTRA_CA_CERTS = join(home, 'model-validation-root.pem')
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'], loopbackFetchOrigin: input.origin })
if (input.secure) {
  const secure = JSON.parse(JSON.stringify(input.secure))
  mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'owned-fake-model-credentials', read: () => secure, readAsync: async () => secure, update: () => { throw new Error('Model command may not install credentials') }, delete: () => { throw new Error('Model command may not delete credentials') } }) }))
}
const transportErrors: any[] = []
const guardedFetch = globalThis.fetch
globalThis.fetch = (async (...args: any[]) => {
  try {
    const response = await (guardedFetch as any)(...args)
    const json = response.json.bind(response)
    response.json = async () => {
      try {
        const bodyReadyPath = response.headers.get('X-M680-Body-Ready')
        if (bodyReadyPath !== null && bodyReadyPath !== '/m680-cut-json-body-ready') throw new Error('Unexpected model-validation body phase acknowledgement')
        const body = json()
        if (bodyReadyPath !== null) {
          // The actual SDK has received headers and entered its actual JSON
          // reader. Let the TLS peer truncate only after that observable phase.
          // Use the existing same-origin capability, leaving parsing unchanged.
          // Keep the original client's verified TLS/CA options for this ACK.
          const acknowledgement = (guardedFetch as any)(new URL(bodyReadyPath, input.origin), { method: 'POST', tls: args[1]?.tls }).then((response: Response) => {
            if (response.status !== 204) throw new Error('Model-validation TLS peer rejected body phase acknowledgement')
          })
          const [result] = await Promise.all([body, acknowledgement])
          return result
        }
        return await body
      } catch (error: any) {
        transportErrors.push({ phase: 'body', name: error.name, message: error.message, fields: Object.fromEntries(Object.keys(error).map(key => [key, error[key]])) })
        throw error
      }
    }
    return response
  } catch (error: any) {
    transportErrors.push({ phase: 'headers', name: error.name, message: error.message, fields: Object.fromEntries(Object.keys(error).map(key => [key, error[key]])) })
    throw error
  }
}) as typeof fetch
// Source probes its separate legacy API-key entry as well as secureStorage.
// Supply only that external keychain prefetch's empty result; all original
// auth selection, cached identity and model checks still execute unchanged.
const legacyReads: string[] = []
mock.module('../../src/utils/secureStorage/keychainPrefetch', () => ({
  getLegacyApiKeyPrefetchResult: () => { legacyReads.push('empty-prefetched-api-key'); return { stdout: null } },
  clearLegacyApiKeyPrefetch: () => {},
  startKeychainPrefetch: () => { throw new Error('No actual keychain prefetch in fixture') },
  ensureKeychainPrefetchCompleted: async () => {},
}))
await import('../../preload')
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(true)
const config = await import('../../src/utils/config')
config.enableConfigs()
const { normalizeApiKeyForConfig } = await import('../../src/utils/authPortable')
config.saveGlobalConfig(current => ({ ...current, customApiKeyResponses: { approved: [normalizeApiKeyForConfig(process.env.ANTHROPIC_API_KEY!)], rejected: [] } }))
const cache = await import('../../src/utils/settings/settingsCache')
const { createStore } = await import('../../src/state/store')
const { getDefaultAppState, AppStoreContext } = await import('../../src/state/AppState')
const { onChangeAppState } = await import('../../src/state/onChangeAppState')
const model = await import('../../src/utils/model/model')
const fastMode = await import('../../src/utils/fastMode')
const React = await import('react')
const { call } = await import('../../src/commands/model/model')
const { default: modelCommand } = await import('../../src/commands/model')
const { processSlashCommand } = await import('../../src/utils/processUserInput/processSlashCommand')
const { normalizeMessagesForAPI } = await import('../../src/utils/messages')
const internals = (React as any).__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE
const previous = internals.H
const rows = []
let previousStore: any
const initialEnvironment = { ...process.env }
const environmentKeys = new Set(['ANTHROPIC_API_KEY', 'CLAUDE_CODE_ENTRYPOINT', 'ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_FABLE_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'ANTHROPIC_CUSTOM_MODEL_OPTION', 'CLAUDE_CODE_DISABLE_1M_CONTEXT', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'CLAUDE_CODE_MAX_RETRIES', 'CC_HAHA_SEND_DISABLED_THINKING', 'ANTHROPIC_BETAS', 'ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES', 'ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES'])
try {
  for (const row of input.rows) {
    const transportStart = transportErrors.length
    for (const key of environmentKeys) {
      if (initialEnvironment[key] === undefined) delete process.env[key]
      else process.env[key] = initialEnvironment[key]
    }
    for (const [key, value] of Object.entries(row.env ?? {})) {
      if (!environmentKeys.has(key) || typeof value !== 'string') throw new Error('Unrelated model-command environment input: ' + key)
      process.env[key] = value
    }
    fs.mkdirSync(join(home, '.claude'), { recursive: true })
    if (!row.continueStore) fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(row.settings))
    cache.resetSettingsCache()
    bootstrap.resetModelStringsForTestingOnly()
    if (!row.continueStore) bootstrap.setMainLoopModelOverride(row.state.mainLoopModel)
    if (row.global) config.saveGlobalConfig(current => ({ ...current, ...row.global }))
    if (row.initializeFast) fastMode.resolveFastModeStatusFromCache()
    // Match the real startup model producer's allowlist filtering before the
    // original JSX consumer sees AppState. Later state transitions stay raw.
    const initialModel = model.getUserSpecifiedModelSetting() ?? null
    const store = row.continueStore ? previousStore : createStore({ ...getDefaultAppState(), ...row.state, mainLoopModel: initialModel, ...(row.initializeFast ? { fastMode: fastMode.getInitialFastModeSetting(initialModel) } : {}) }, onChangeAppState)
    if (!store) throw new Error('No actual prior store for continuation')
    previousStore = store
    let observed: any
    const effects: Array<() => void> = []
    internals.H = {
      useMemoCache: (n: number) => Array(n).fill(Symbol.for('react.memo_cache_sentinel')),
      useContext: (context: unknown) => context === AppStoreContext ? store : null,
      useSyncExternalStore: (_subscribe: unknown, snapshot: () => unknown) => snapshot(),
      useEffect: (effect: () => void) => effects.push(effect),
      useCallback: (callback: unknown) => callback,
    }
    // The descriptor, original local JSX dispatcher and generic completion
    // producer all run. The wrapper observes onDone and forwards it unchanged.
    const descriptor = { ...modelCommand, load: async () => ({ call: (onDone: any, context: any, args: string) => call((notice, options) => { observed = { notice, options: options ?? null }; onDone(notice, options) }, context, args) }) }
    const result = await processSlashCommand('/model ' + row.args, [], [], [], { options: { commands: [descriptor], isNonInteractiveSession: false }, getAppState: async () => store.getState() } as any, (pending: any) => {
      if (pending?.jsx) {
        const child = pending.jsx.type(pending.jsx.props)
        if (row.selectModel !== undefined) {
          if (!child?.props?.onSelect) throw new Error('Original model wrapper did not produce selector callback')
          child.props.onSelect(row.selectModel)
        }
        for (const effect of effects.splice(0)) effect()
      }
    })
    const cleanups: Array<() => void> = []
    if (row.notifications) {
      const { useFastModeNotification } = await import('../../src/hooks/notifs/useFastModeNotification')
      useFastModeNotification()
      for (const effect of effects.splice(0)) { const cleanup = effect() as unknown; if (typeof cleanup === 'function') cleanups.push(cleanup as () => void) }
    }
    if (row.ask) {
      const { queryWithModel } = await import('../../src/services/api/claude')
      const current = store.getState()
      await queryWithModel({ userPrompt: 'actual fast model stream', signal: new AbortController().signal, options: { model: model.parseUserSpecifiedModel(current.mainLoopModelForSession ?? current.mainLoopModel ?? model.getDefaultMainLoopModelSetting()), fastMode: current.fastMode, maxOutputTokensOverride: 1, querySource: 'repl_main_thread', isNonInteractiveSession: false, agents: [], mcpTools: [], hasAppendSystemPrompt: false, enablePromptCaching: false } })
    }
    rows.push({ name: row.name, ...observed, transportErrors: transportErrors.slice(transportStart), api: normalizeMessagesForAPI(result.messages).map((message: any) => message.message), shouldQuery: result.shouldQuery, messages: result.messages.map((message: any) => ({ type: message.type, ...(message.subtype ? { subtype: message.subtype } : {}), ...(message.message ? { message: message.message } : { content: message.content }), ...(message.isMeta !== undefined ? { isMeta: message.isMeta } : {}) })), state: {
      mainLoopModel: store.getState().mainLoopModel,
      mainLoopModelForSession: store.getState().mainLoopModelForSession,
      fastMode: store.getState().fastMode,
      effortValue: store.getState().effortValue ?? null,
      ...(row.notifications ? { notifications: store.getState().notifications } : {}),
    }, settings: JSON.parse(fs.readFileSync(join(home, '.claude/settings.json'), 'utf8')) })
    for (const cleanup of cleanups) cleanup()
  }
} finally {
  internals.H = previous
}
if (capabilities.blocked.length) throw new Error('Original model fixture capability denied: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ rows, transportErrors, capabilities: { blocked: capabilities.blocked, legacyReads } }) + '\n')
