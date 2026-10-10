import { spyOn } from 'bun:test'
import { feature } from 'bun:bundle'
import * as crypto from 'node:crypto'
import { join, sep } from 'node:path'
import { lstatSync, realpathSync } from 'node:fs'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string, classifier: boolean, origin: string, promptSource: string, runtime: { platform: string, arch: string, version: string } }
const home = assertDisposableFixtureHome(input.home, 'Session resume request')
const origin = new URL(input.origin)
if (process.env.CC_HAHA_SESSION_RESUME_REQUEST !== '1' || process.env.NODE_ENV !== 'production' || input.classifier !== true || !feature('TRANSCRIPT_CLASSIFIER') || origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || origin.origin !== input.origin || input.origin !== process.env.ANTHROPIC_BASE_URL || typeof input.promptSource !== 'string' || input.runtime.platform !== process.platform || input.runtime.arch !== process.arch || input.runtime.version !== process.version || Object.keys(input).some(name => !['home', 'classifier', 'origin', 'promptSource', 'runtime'].includes(name))) throw new Error('Session resume request requires original private loopback inputs')
for (const path of [join(home, 'session.jsonl'), join(home, '.claude', '.claude.json')]) {
  const info = lstatSync(path)
  if (!info.isFile() || info.isSymbolicLink() || !realpathSync(path).startsWith(realpathSync(home) + sep)) throw new Error('Session resume request requires ordinary private files')
}
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.origin })
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home)
bootstrap.setIsInteractive(false); bootstrap.setSdkBetas([]); bootstrap.setPromptCache1hEligible(false); bootstrap.setPromptCache1hAllowlist([])
const recovery = await import('../../../src/utils/conversationRecovery'), messages = await import('../../../src/utils/messages')
const config = await import('../../../src/utils/config'); config.enableConfigs()
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { runWithWorkload } = await import('../../../src/utils/workloadContext')
const allocations: unknown[] = []; let uuid = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => { const value = '00000000-0000-4000-8000-' + (++uuid).toString().padStart(12, '0'); allocations.push({ kind: 'uuid', value }); return value as ReturnType<typeof crypto.randomUUID> })
const OriginalDate = Date
globalThis.Date = class extends OriginalDate { constructor(...args: unknown[]) { super(...(args.length ? args : [1_700_000_000_000]) as [number]); if (!args.length) allocations.push({ kind: 'time', value: this.toISOString() }) } static now() { return 1_700_000_000_000 } } as DateConstructor
const loaded = await recovery.loadMessagesFromJsonlPath(join(home, 'session.jsonl'))
const result = recovery.deserializeMessagesWithInterruptDetection(loaded.messages)
const resultSource = JSON.stringify({ ...result, sessionId: loaded.sessionId })
if (!loaded.sessionId) throw new Error('Source request requires actual recovered SID')
bootstrap.switchSession(loaded.sessionId)
const history = [...result.messages, messages.createUserMessage({ content: JSON.parse(input.promptSource) })]
const generation = structuredClone(allocations), historyBefore = history.map(message => JSON.stringify(message))
const requests: unknown[] = [], prefetchAllocations: unknown[] = [], yields: unknown[] = []
await runWithWorkload(undefined, async () => {
  for await (const event of queryModelWithStreaming({ messages: history, systemPrompt: asSystemPrompt([]), thinkingConfig: { type: 'disabled' }, tools: [], signal: new AbortController().signal,
    options: { model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
      fetchOverride: async (url, init) => { const parsed = new URL(String(url)); if (parsed.origin !== input.origin || init?.method !== 'POST') throw new Error('Resume request escaped exact loopback POST'); prefetchAllocations.push(structuredClone(allocations)); requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) }); return fetch(url, init) },
    },
  })) yields.push(JSON.stringify(event))
})
if (capabilities.blocked.length) throw new Error('Resume request external capability denied: ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ observation: { resultSource, generation, historyBefore, requests }, sourceOnly: { capabilities, prefetchAllocations, yields, runtime: input.runtime } }))
