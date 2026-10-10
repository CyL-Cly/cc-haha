import { lstatSync, realpathSync } from 'node:fs'
import * as fs from 'node:fs/promises'
import { dirname, isAbsolute, join, relative, sep } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

type Step = { operation: 'resume' | 'path' | 'transcript' | 'locate' | 'rewrite' | 'append', sessionId?: string, project?: string, bytes?: string, keepAllLeaves?: boolean }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string, sessionId: string, project: string, files: Array<{ project: string, name: string }>, steps: Step[], disablePrecompactSkip?: boolean }
const home = assertDisposableFixtureHome(input.home, 'Session history')
const canonicalHome = realpathSync(home)
const output = join(home, 'history-result.json')
function assertFile(path: string) {
  let probe = path
  for (;;) {
    try {
      const info = lstatSync(probe)
      const child = relative(canonicalHome, realpathSync(probe))
      if (info.isSymbolicLink() || child === '..' || child.startsWith('..' + sep) || isAbsolute(child) || probe === path && !info.isFile()) throw new Error('Session history fixture requires ordinary private files')
      return
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT' || dirname(probe) === probe) throw error
      probe = dirname(probe)
    }
  }
}
const file = (project = input.project, sessionId = input.sessionId) => join(home, '.claude', 'projects', project, sessionId + '.jsonl')
for (const entry of input.files) assertFile(join(home, '.claude', 'projects', entry.project, entry.name))
assertFile(output)
process.env.CLAUDE_CODE_DISABLE_PRECOMPACT_SKIP = input.disablePrecompactSkip ? '1' : '0'
const capabilities = await installClientFixtureCapabilities(home)
const { SessionService } = await import('../../../src/server/services/sessionService')
const { loadMessagesFromJsonlPath } = await import('../../../src/utils/conversationRecovery')
const { loadTranscriptFile } = await import('../../../src/utils/sessionStorage')
const { attachAnalyticsSink } = await import('../../../src/services/analytics')
const { attachErrorLogSink } = await import('../../../src/utils/log')
const events: Array<{ eventName: string, metadata: unknown }> = []
const rawErrors: unknown[] = []
attachAnalyticsSink({
  logEvent: (eventName, metadata) => { events.push({ eventName, metadata }) },
  logEventAsync: async (eventName, metadata) => { events.push({ eventName, metadata }) },
})
attachErrorLogSink({
  logError: error => { rawErrors.push({ name: error.name, message: error.message, stack: error.stack }) },
  logMCPError: (serverName, error) => { rawErrors.push({ serverName, error: String(error) }) },
  logMCPDebug: (serverName, message) => { rawErrors.push({ serverName, message }) },
  getErrorsPath: () => join(home, 'errors'),
  getMCPLogsPath: serverName => join(home, 'mcp-' + serverName),
})
const service = new SessionService()
const results: unknown[] = []
for (const [index, step] of input.steps.entries()) {
  const sessionId = step.sessionId ?? input.sessionId
  const path = file(step.project, sessionId)
  try {
    let result: unknown
    if (step.operation === 'append' || step.operation === 'rewrite') {
      assertFile(path)
      await (step.operation === 'append' ? fs.appendFile : fs.writeFile)(path, Buffer.from(step.bytes ?? '', 'hex'))
      await fs.utimes(path, 1_700_000_100 + index, 1_700_000_100 + index)
      result = { ok: true }
    } else if (step.operation === 'transcript') {
      assertFile(path)
      const loaded = await loadTranscriptFile(path, { keepAllLeaves: step.keepAllLeaves })
      result = Object.fromEntries(Object.entries(loaded).map(([key, value]) => [key, value instanceof Map ? [...value] : value instanceof Set ? [...value] : value]))
    } else if (step.operation === 'path') {
      assertFile(path)
      result = await loadMessagesFromJsonlPath(path)
    } else {
      const located = await service.findSessionFile(sessionId)
      if (step.operation === 'locate') result = located
      else result = { located, ...(located ? await loadMessagesFromJsonlPath(located.filePath) : { messages: [] }) }
    }
    results.push({ bytes: Buffer.from(JSON.stringify(result)).toString('hex') })
  } catch (error) {
    const failure = error as { name?: string, code?: string, message?: string }
    rawErrors.push({ name: failure.name, message: failure.message, stack: (error as Error).stack })
    results.push({ error: { name: failure.name, code: failure.code ?? 'ERROR', message: failure.message } })
  }
}
if (capabilities.blocked.length) throw new Error('Session history fixture blocked external capability: ' + capabilities.blocked.join(', '))
assertFile(output)
await fs.writeFile(output, JSON.stringify({ results, events, rawErrors }))
console.log(JSON.stringify({ result: 'history-result.json' }))
