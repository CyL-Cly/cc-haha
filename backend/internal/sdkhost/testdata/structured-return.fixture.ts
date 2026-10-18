import fs from 'node:fs'
import { dirname, join, isAbsolute } from 'node:path'

const input = await Bun.stdin.json()
if (!isAbsolute(input.sourceRoot) || process.env.CC_HAHA_STRUCTURED_RETURN_CONTRACT !== '1' || Bun.version !== '1.3.14') throw new Error('Owned pinned Source return fixture required')
if (process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1' || process.env.ANTHROPIC_API_KEY !== 'offline-fixture-key' || process.env.ANTHROPIC_AUTH_TOKEN || process.env.CLAUDE_CODE_OAUTH_TOKEN) throw new Error('Hermetic production environment with fake API key required')
const { assertDisposableFixtureHome } = await import(join(input.sourceRoot, 'scripts/quality-gate/go-parity/fixture-guard.ts'))
const { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } = await import(join(input.sourceRoot, 'scripts/quality-gate/go-parity/client-options-capabilities.ts'))
const home = assertDisposableFixtureHome(input.home, 'StructuredIO return contract')
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) if (!fs.existsSync(join(path, suffix))) absent.push(join(path, suffix))
  if (dirname(path) === path) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent })
;(globalThis as any).MACRO = { VERSION: '2.1.288', BUILD_TIME: '', PACKAGE_URL: '' }
await import(join(input.sourceRoot, 'preload.ts'))
const bootstrap = await import(join(input.sourceRoot, 'src/bootstrap/state.ts'))
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.setAllowedSettingSources(['userSettings'])
bootstrap.setIsInteractive(false)
const { StructuredIO } = await import(join(input.sourceRoot, 'src/cli/structuredIO.ts'))
const resultDescriptor = (result: IteratorResult<any>) => ({ done: result.done === true, defined: result.value !== undefined, ...(result.value !== undefined ? { value: result.value } : {}) })
const rows: any[] = []
for (const profile of input.profiles) {
  const events: string[] = []
  const counters = { entered: 0, yielded: 0, resumed: 0, finallyEntered: 0, finallyCompleted: 0 }
  const bodyError = new Error('actual-raw-body-error')
  const closeError = new Error('actual-raw-finally-error')
  async function* rawProducer(): AsyncGenerator<string> {
    counters.entered++
    events.push('raw:entry')
    try {
      for (const chunk of profile.chunks) {
        counters.yielded++
        events.push('raw:yield')
        yield chunk
        counters.resumed++
        events.push('raw:resumed')
      }
      if (profile.bodyError) throw bodyError
    } finally {
      counters.finallyEntered++
      events.push('raw:finally')
      if (profile.finallyMode === 'throw') throw closeError
      if (profile.finallyMode === 'yield') {
        events.push('raw:finally-yield')
        yield 'actual-finally-yield'
      }
      counters.finallyCompleted++
      events.push('raw:finally-complete')
    }
  }
  const io = new StructuredIO(rawProducer())
  if (profile.prepend) io.prependUserMessage('actual-prepended-prompt')
  const sandbox = io.createSandboxAskCallback()({ host: 'pending.return.test' })
  await io.outbound.next() // actual request admission, not a fabricated response
  const results: any[] = []
  const errorDescriptor = (error: unknown) => ({ error: error === bodyError ? 'body-identity' : error === closeError ? 'close-identity' : 'other', message: error instanceof Error ? error.message : String(error) })
  async function consume(pending: Promise<IteratorResult<any>>) {
    try { return resultDescriptor(await pending) } catch (error) { return errorDescriptor(error) }
  }
  const requestReturn = () => profile.returnDefined ? io.structuredInput.return({ kind: 'actual-return-value' }) : io.structuredInput.return(undefined)
  if (profile.consumer === 'before') {
    results.push(await consume(requestReturn()))
    results.push(await consume(io.structuredInput.next()))
  } else if (profile.consumer === 'queued') {
    const first = io.structuredInput.next()
    const returned = requestReturn()
    const last = io.structuredInput.next()
    results.push(await consume(first))
    results.push(await consume(returned))
    results.push(await consume(last))
  } else if (profile.consumer === 'drain') {
    for (let index = 0; index < 8; index++) {
      const result = await consume(io.structuredInput.next())
      results.push(result)
      if ('error' in result || result.done) break
      if (index === 7) throw new Error('Unexpected nonterminal raw source')
    }
    results.push(await consume(io.structuredInput.next()))
  } else {
    results.push(await consume(io.structuredInput.next()))
    results.push(await consume(requestReturn()))
    results.push(await consume(io.structuredInput.next()))
  }
  let sandboxResult: boolean | null = null
  if (profile.consumer === 'drain' && !profile.bodyError && profile.finallyMode !== 'throw') sandboxResult = await sandbox
  const pendingBeforeNew = io.getPendingPermissionRequests().length
  const post = io.createSandboxAskCallback()({ host: 'new.return.test' })
  let newResult: boolean | null = null
  if (profile.consumer === 'drain' && !profile.bodyError && profile.finallyMode !== 'throw') newResult = await post
  else await io.outbound.next() // admission stays open after abrupt completion
  rows.push({ name: profile.name, results, events, counters, pendingBeforeNew, pendingAfterNew: io.getPendingPermissionRequests().length, sandboxResult, newResult })
}
let headless: any = null
if (input.headless) {
  // Same real startup permission used by the existing Source SDK fixtures.
  // This is enabled only for the actual public print consumer, after all
  // constructor profiles, and all config paths remain below disposable HOME.
  const config = await import(join(input.sourceRoot, 'src/utils/config.ts'))
  config.enableConfigs()
  const { __runHeadlessStreamingForTests } = await import(join(input.sourceRoot, 'src/cli/print.ts'))
  const { getDefaultAppState } = await import(join(input.sourceRoot, 'src/state/AppStateStore.ts'))
  const events: string[] = []
  async function* raw() {
    events.push('raw:entry')
    try {
      yield JSON.stringify({ type: 'control_request', request_id: 'actual-end', request: { subtype: 'end_session' } }) + '\n'
      events.push('raw:resumed')
      yield JSON.stringify({ type: 'user', message: { role: 'user', content: 'must-not-be-read' } }) + '\n'
    } finally { events.push('raw:finally') }
  }
  const io = new StructuredIO(raw())
  let state = getDefaultAppState()
  const responses: any[] = []
  const output = __runHeadlessStreamingForTests(io, [], [], [], [], (() => { throw new Error('No tool/model expected') }) as any, {}, () => state, (update: any) => { state = update(state) }, [], { outputFormat: 'stream-json' })
  for await (const message of output) {
    if (message.type === 'control_response') responses.push(message.response)
  }
  headless = { events, responses }
}
if (caps.blocked.length) throw new Error('Structured return fixture attempted unowned capability')
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ rows, headless, blocked: caps.blocked }) + '\n', error => error ? reject(error) : resolve()))
