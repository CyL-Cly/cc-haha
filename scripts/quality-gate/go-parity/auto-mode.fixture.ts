import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { userInfo } from 'node:os'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

type Input = { home: string; args: string[]; entry: boolean; operation: 'defaults' | 'config'; sources: Record<string, unknown>; internal?: boolean }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Input
const home = assertDisposableFixtureHome(input.home, 'auto-mode')
const network: { method: string; path: string }[] = []
const received: { method: string; path: string }[] = []
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
  received.push({ method: request.method, path: new URL(request.url).pathname })
  return new Response('', { status: 200 })
} })
const origin = `http://127.0.0.1:${server.port}`
process.env.ANTHROPIC_BASE_URL = origin
process.env.CLAUDE_CODE_SIMPLE = '1'
if (input.internal) process.env.USER_TYPE = 'ant'
// The real repository detector walks ancestors. Each exact ancestor .git
// path gets an absent external adapter; the fixture never reads those paths.
const ancestorGit: string[] = []
for (let directory = dirname(fs.realpathSync(home)); ; directory = dirname(directory)) {
  ancestorGit.push(join(directory, '.git'))
  if (dirname(directory) === directory) break
}
const capabilities = await installClientFixtureCapabilities(home, {
  loopbackFetchOrigin: origin,
  absentPaths: [...absentManagedSettingsFixturePaths(), ...ancestorGit,
    `/Library/Managed Preferences/${userInfo().username}/com.anthropic.claudecode.plist`,
    '/Library/Managed Preferences/com.anthropic.claudecode.plist'],
})
// Source init deliberately fires preconnect without awaiting its response. Its
// fetch invocation is observable before process.exit; physical receipt can race
// the short config handler and is retained separately, never asserted as a
// completed Source request. Keep the exact-origin capability wrapper underneath.
const guardedFetch = globalThis.fetch
globalThis.fetch = ((value: Parameters<typeof fetch>[0], init?: RequestInit) => {
  const url = new URL(value instanceof Request ? value.url : String(value))
  network.push({ method: init?.method ?? (value instanceof Request ? value.method : 'GET'), path: url.pathname })
  return guardedFetch(value, init)
}) as typeof fetch
const checkpoints: string[] = []
process.on('exit', code => fs.writeFileSync(join(home, 'auto-mode-evidence.json'), JSON.stringify({ code, network, received, checkpoints, ...capabilities })))
process.argv = [process.execPath, new URL('../../../src/entrypoints/cli.tsx', import.meta.url).pathname, ...input.args]
await import('../../../preload')
if (input.entry) {
  const profiler = await import('../../../src/utils/startupProfiler')
  const original = profiler.profileCheckpoint
  spyOn(profiler, 'profileCheckpoint').mockImplementation(name => { checkpoints.push(name); original(name) })
  await import('../../../src/entrypoints/cli')
} else {
  // Match the real CLI's module load order: direct loading of settings before
  // main triggers a Source classifier circular-import TDZ in this build.
  await import('../../../src/main')
  // Freeze only the raw settings source input. Trust filtering, schema
  // validation, merging, expansion and the CLI handlers are real Source code.
  const settings = await import('../../../src/utils/settings/settings')
  spyOn(settings, 'getSettingsForSource').mockImplementation(source => input.sources[source] as any)
  const handlers = await import('../../../src/cli/handlers/autoMode')
  if (input.operation === 'defaults') handlers.autoModeDefaultsHandler()
  else handlers.autoModeConfigHandler()
  server.stop(true)
}
