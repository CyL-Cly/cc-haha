import fs from 'node:fs'
import path from 'node:path'
import { mock, spyOn } from 'bun:test'
import { syncBuiltinESMExports } from 'node:module'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

// Run the actual public CLI with original stdin/EOF, not a schema-only oracle.
const input = JSON.parse(process.env.CC_HAHA_AUTO_COMMAND_INPUT ?? 'null')
const home = assertDisposableFixtureHome(input.homeDir, 'Actual public auto-mode CLI')
if (process.env.CC_HAHA_AUTO_COMMAND_CLI_FIXTURE !== '1') throw new Error('Missing auto-mode CLI fixture')
const requests: any[] = []
const subprocesses: any[] = []
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const endpoint = new URL(request.url).pathname
  if (endpoint !== '/v1/messages') return new Response('{}', { status: 404 })
  const body = await request.json()
  requests.push(body)
  const status = input.status ?? 200
  const response = status === 200 ? {
    id: 'actual-auto-critique-response', type: 'message', role: 'assistant', model: 'fixture-model',
    content: input.content ?? [{ type: 'text', text: 'PUBLIC_AUTO_RULES_CRITIQUE_OK' }],
    stop_reason: 'end_turn', stop_sequence: null,
    usage: { input_tokens: 10, output_tokens: 5 },
  } : { type: 'error', error: { type: 'authentication_error', message: 'private credential rejected' } }
  return Response.json(response, { status })
} })
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token', path.join(path.dirname(process.execPath), 'rg'), '/usr/bin/rg', '/bin/rg']
for (const suffix of ['.claude/skills', '.claude/rules', 'CLAUDE.md']) absent.push(path.join('/Library/Application Support/ClaudeCode', suffix))
for (const start of [home, fs.realpathSync(home)]) {
  for (let directory = path.dirname(start); ; directory = path.dirname(directory)) {
    for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.claude/workflows', '.agents/skills', '.agents/commands']) absent.push(path.join(directory, suffix))
    if (directory === path.dirname(directory)) break
  }
}
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: server.url.origin, absentPaths: absent })
const processes = require('node:child_process')
const guardedExec = processes.exec
const guardedSpawn = processes.spawn
processes.spawn = (...args: any[]) => {
  subprocesses.push({ kind: 'spawn', command: args[0], args: args[1] })
  // The private machine has no IDE processes. Bound only this original
  // read-only discovery primitive; all model and tool execution stays real.
  if (typeof args[0] === 'string' && args[0].startsWith('ps aux | grep -E "Visual Studio Code|Code Helper|Cursor Helper|Windsurf Helper|') && args[0].endsWith('" | grep -v grep') && Array.isArray(args[1]) && args[1].length === 0) {
    const child = new EventEmitter() as any
    child.stdout = new PassThrough()
    child.stderr = new PassThrough()
    child.stdin = new PassThrough()
    child.stdio = [child.stdin, child.stdout, child.stderr]
    child.kill = () => true
    queueMicrotask(() => { child.stdout.end(); child.stderr.end(); child.exitCode = 1; child.emit('exit', 1, null); child.emit('close', 1, null) })
    return child
  }
  return guardedSpawn(...args)
}
const processExports = await import('node:child_process')
spyOn(processExports, 'spawn').mockImplementation(processes.spawn)
processes.exec = (...args: any[]) => {
  subprocesses.push({ command: args[0] })
  // The only OS identity primitive is an isolated fake machine serial.
  // Every other subprocess stays behind the original deny boundary.
  if (args[0] === 'ioreg -rd1 -c "IOPlatformExpertDevice"') {
    const callback = args.findLast(value => typeof value === 'function')
    queueMicrotask(() => callback?.(null, '"IOPlatformSerialNumber" = "private-auto-command-fixture-serial"\n', ''))
    return { kill() {} }
  }
  return guardedExec(...args)
}
syncBuiltinESMExports()
// The external OS managed-store primitive is absent. CLI/permission/schema/
// QueryEngine/tool execution/result and transcript/Close remain original.
mock.module('../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
process.env.ANTHROPIC_BASE_URL = server.url.origin
process.env.ANTHROPIC_API_KEY = 'fake-original-schema-cli-key'
// Ordinary command startup reads the isolated trusted rules; no bare mode.
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../preload')
const state = await import('../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
process.argv = [process.execPath, new URL('../../src/entrypoints/cli.tsx', import.meta.url).pathname, ...input.args]
process.on('exit', code => fs.writeFileSync(path.join(home, 'source-auto-command-receipt.json'), JSON.stringify({ code, requests, subprocesses, capabilities, sessionId: state.getSessionId() })))
setTimeout(() => process.exit(94), 25000).unref()
// Execute the actual original Commander action and sideQuery SDK consumer.
// The loopback peer is deterministic; no production command or model stub is used.
try {
  const { main } = await import('../../src/main')
  await main()
} catch (error) {
  console.error(error instanceof Error ? `${error.name}: ${error.message}` : String(error))
  process.exit(1)
}
