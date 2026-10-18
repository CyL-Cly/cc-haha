import { mock } from 'bun:test'
import fs from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual MCP diagnostic sink')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_MCP_ERROR_SINK !== '1') throw new Error('Pinned private MCP sink required')
const caps = await installClientFixtureCapabilities(home)
const promises = await import('fs/promises')
const originalUnlink = promises.unlink, originalSymlink = promises.symlink
let latestStarted = false
let latestSettled!: () => void
const latestCompletion = new Promise<void>(resolve => { latestSettled = resolve })
mock.module('fs/promises', () => ({ ...promises,
  unlink: (path: any) => { if (String(path).endsWith('/latest')) latestStarted = true; return originalUnlink(path) },
  symlink: async (...args: any[]) => { try { return await originalSymlink(...args as [any, any]) } finally { if (String(args[1]).endsWith('/latest')) latestSettled() } },
}))
await import('../../../preload')
const actualDate = Date
globalThis.Date = class extends actualDate { constructor(...args: any[]) { super(...(args.length ? args : [1700000000000]) as [any]) } static now() { return 1700000000000 } } as DateConstructor
Object.assign(process.env, input.environment)
const state = await import('../../../src/bootstrap/state')
mock.module('../../../src/bootstrap/state', () => ({ ...state, getSessionId: () => 'private-mcp-log-session' }))
const operations = await import('../../../src/utils/fsOperations')
const actualFiles = operations.getFsImplementation()
mock.module('../../../src/utils/fsOperations', () => ({ ...operations, getFsImplementation: () => ({ ...actualFiles, cwd: () => input.cwd }) }))
const log = await import('../../../src/utils/log')
const sink = await import('../../../src/utils/errorLogSink')
const debug = await import('../../../src/utils/debug')
sink.initializeErrorLogSink()
log.logMCPDebug(input.server, 'OAuth complete, reconnected with 1 tool(s)')
log.logMCPError(input.server, 'OAuth flow failed after tool-triggered start: private denied')
const error = new Error('private original message')
error.name = 'MCPError'
error.stack = 'MCPError: private original message\n    at private-owned-frame'
log.logMCPError(input.server, error)
const snapshot = (): Record<string, string> => {
  const result: Record<string, string> = {}
  const walk = (directory: string) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const path = join(directory, entry.name)
      if (entry.isSymbolicLink()) result[path] = 'link:' + fs.readlinkSync(path)
      else if (entry.isDirectory()) walk(path)
      else result[path] = fs.readFileSync(path, 'utf8')
    }
  }
  walk(process.env.CLAUDE_CONFIG_DIR!)
  return result
}
sink._flushLogWritersForTesting()
await debug.flushDebugLogs()
if (latestStarted) await latestCompletion
const files = snapshot()
sink._clearLogWritersForTesting()
if (caps.blocked.length) throw new Error('MCP log escaped private home: ' + caps.blocked.join(', '))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ files, memory: log.getInMemoryErrors(), blocked: caps.blocked }), () => resolve()))
process.exit(0)
