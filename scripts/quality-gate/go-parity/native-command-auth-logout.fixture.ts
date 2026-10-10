import { assertDisposableFixtureHome } from './fixture-guard'
import { mock, spyOn } from 'bun:test'
import { join } from 'node:path'
import { mkdir, readFile, unlink, writeFile } from 'node:fs/promises'

const home = assertDisposableFixtureHome(process.env.HOME, 'CLI logout state transitions')
if (process.env.CC_HAHA_COMMAND_LOGOUT_FIXTURE !== '1') throw new Error('Unowned logout fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let current: any, calls: string[] = []
const config = await import('../../../src/utils/config')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/config', () => ({ ...config, getGlobalConfig: () => current.global, saveGlobalConfig: (update: any) => { calls.push('global-save'); current.global = update(current.global) } }))
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({
  read: () => structuredClone(current.secure),
  update: (next: any) => { if (!current.updateFailure) current.secure = next; return { success: !current.updateFailure } },
  delete: () => { calls.push('secure-delete'); current.secure = null; return !current.deleteFailure },
}) }))
mock.module('../../../src/utils/authPortable', () => ({ normalizeApiKeyForConfig: (key: string) => key.slice(-20), maybeRemoveApiKeyFromMacOSKeychainThrows: async () => { calls.push('legacy-delete'); throw new Error('isolated unavailable') } }))
mock.module('../../../src/utils/telemetry/instrumentation', () => ({ flushTelemetry: async () => { calls.push('flush-empty') } }))
mock.module('../../../src/services/analytics/growthbook', () => ({ refreshGrowthBookAfterAuthChange: () => { calls.push('refresh-auth-cache') } }))
const handler = await import('../../../src/cli/handlers/auth')
const storage = await import('../../../src/services/openaiAuth/storage')
const write = process.stdout.write.bind(process.stdout)
let stdout = '', stderr = ''
class HandlerExit { constructor(readonly code: number) {} }
spyOn(process.stdout, 'write').mockImplementation(((chunk: any) => { stdout += Buffer.from(chunk).toString('utf8'); return true }) as any)
spyOn(process.stderr, 'write').mockImplementation(((chunk: any) => { stderr += Buffer.from(chunk).toString('utf8'); return true }) as any)
spyOn(process, 'exit').mockImplementation(((code: number) => { throw new HandlerExit(code) }) as any)
const output = [], marker = join(home, '.claude/cc-haha/openai-oauth-file-backed'), token = join(home, 'selected-openai.json'), remote = join(home, '.claude/remote-settings.json')
await mkdir(join(home, '.claude/cc-haha'), { recursive: true })
for (const row of rows) {
  current = structuredClone(row)
  await unlink(marker).catch(() => {})
  await unlink(token).catch(() => {})
  await writeFile(remote, '{}')
  if (row.fileBacked) { process.env.OPENAI_CODEX_OAUTH_FILE = token; await writeFile(token, JSON.stringify(row.token)) }
  else delete process.env.OPENAI_CODEX_OAUTH_FILE
  storage.clearOpenAIOAuthTokenCache()
  calls = []; stdout = ''; stderr = ''
  let exitCode: number | null = null, failure: unknown
  try { await handler.authLogout({ openai: !!row.openai }) }
  catch (error) { if (error instanceof HandlerExit) exitCode = error.code; else failure = { name: (error as Error).name, message: (error as Error).message } }
  output.push({ stdout, stderr, exitCode, secure: current.secure, global: current.global, marker: await readFile(marker, 'utf8').catch(() => null), token: await readFile(token, 'utf8').catch(() => null), remote: await readFile(remote, 'utf8').catch(() => null), calls, ...(failure ? { failure } : {}) })
}
write(JSON.stringify(output))
