import fs from 'node:fs'
import path from 'node:path'
import { mock, spyOn } from 'bun:test'
import { syncBuiltinESMExports } from 'node:module'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

// Run the original public Commander/action, startup, real debug sink and print consumer.
const input = JSON.parse(process.env.CC_HAHA_DEBUG_CLI_INPUT ?? 'null')
const home = assertDisposableFixtureHome(input.homeDir, 'Actual debug CLI')
if (process.env.CC_HAHA_DEBUG_CLI_FIXTURE !== '1') throw new Error('Missing debug CLI fixture')
const requests: any[] = []
const subprocesses: any[] = []
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const endpoint = new URL(request.url).pathname
  if (endpoint !== '/v1/messages') return new Response('{}', { status: 404 })
  const body = await request.json()
  requests.push(body)
  const id = `debug-${requests.length}`
  const events = [
    ['message_start', { type: 'message_start', message: { id, type: 'message', role: 'assistant', model: 'fixture-model', content: [], usage: { input_tokens: 1, output_tokens: 0 } } }],
    ['content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } }],
    ['content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'ACTUAL_DEBUG_DONE' } }],
    ['content_block_stop', { type: 'content_block_stop', index: 0 }],
    ['message_delta', { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 1 } }],
    ['message_stop', { type: 'message_stop' }],
  ]
  return new Response(events.map(([name, data]) => `event: ${name}\ndata: ${JSON.stringify(data)}\n\n`).join(''), { headers: { 'content-type': 'text/event-stream' } })
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
    queueMicrotask(() => callback?.(null, '"IOPlatformSerialNumber" = "private-debug-fixture-serial"\n', ''))
    return { kill() {} }
  }
  return guardedExec(...args)
}
syncBuiltinESMExports()
// The external OS managed-store primitive is absent. CLI/permission/debug/
// QueryEngine/tool execution/result and transcript/Close remain original.
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
process.env.ANTHROPIC_BASE_URL = server.url.origin
process.env.ANTHROPIC_API_KEY = 'fake-original-debug-cli-key'
process.env.CLAUDE_CODE_SIMPLE = '1'
;(globalThis as any).MACRO = { VERSION: 'source-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
Object.assign(process.env, input.environment ?? {})
process.argv = [process.execPath, new URL('../../../src/entrypoints/cli.tsx', import.meta.url).pathname, '--bare', '-p', '--model', 'fixture-model', '--thinking', 'disabled', '--system-prompt', 'Actual debug CLI.', '--no-computer-use', '--tools', 'Read', '--output-format', 'json', '--setting-sources', 'user', ...(input.arguments ?? []), 'Return actual private response']
process.on('exit', code => fs.writeFileSync(path.join(home, 'original-debug-cli-receipt.json'), JSON.stringify({ code, requests, subprocesses, capabilities })))
setTimeout(() => process.exit(94), 25000).unref()
// Await the original exported CLI entry/action so startup rejection is
// observed as a terminal failure rather than held alive by this peer server.
// All Commander flags, debug sink, startup, QueryEngine and result consumers are real.
try {
  const { main } = await import('../../../src/main')
  await main()
} catch (error) {
  console.error(error instanceof Error ? `${error.name}: ${error.message}` : String(error))
  process.exit(1)
}
