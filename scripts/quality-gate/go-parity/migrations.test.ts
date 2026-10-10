import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const fixturePath = join(rootDir, 'scripts/quality-gate/go-parity/migrations.fixture.ts')
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-migrations-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'migrations-fixture.exe' : 'migrations-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/migrations/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { operation: 'ensure' | 'reset' | 'get-user' | 'managed-read' | 'managed-update'; config?: string; patch?: Record<string, unknown> }
type Fault = { operation: string; path: string; message: string; once?: boolean }
type Scenario = { files?: Record<string, string | Uint8Array>; steps?: Step[]; faults?: Fault[]; nativeFailure?: 'syntax' | 'base-url-type' | 'model-type' }
type Report = { migratedEntries: string[]; failures: string[] }
type Output = { results: Array<{ value?: unknown; error?: string }>; trace: Array<{ operation: string; paths: string[] }> }
const defaultSteps: Step[] = [{ operation: 'ensure' }, { operation: 'ensure' }]
const normalizeNonce = (path: string) => path.replace(/\.(bak-before-migration|invalid)-\d+-[a-f0-9]+$/, '.$1-<nonce>').replace(/\.tmp\.[^/]+$/, '.tmp.<nonce>')

async function withHome<T>(task: (home: string) => Promise<T>) {
  const home = await mkdtemp(join(tmpdir(), 'go-migrations-parity-'))
  try { return await task(home) } finally { await rm(home, { recursive: true, force: true }) }
}
async function initialize(home: string, scenario: Scenario) {
  for (const [path, contents] of Object.entries(scenario.files ?? {})) {
    const absolute = join(home, path)
    await mkdir(dirname(absolute), { recursive: true })
    await writeFile(absolute, contents)
  }
}
async function run(kind: 'typescript' | 'go', home: string, scenario: Scenario): Promise<Output> {
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', fixturePath], {
    cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir() }),
    stdin: new Blob([JSON.stringify({ homeDir: home, steps: scenario.steps ?? defaultSteps, faults: scenario.faults ?? [] })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} migration fixture: ${stderr}`).toBe(0)
    const output = JSON.parse(stdout) as Output
    output.trace = output.trace.map(event => ({ ...event, paths: event.paths.map(normalizeNonce) }))
    // Native JSON.parse and TypeError descriptions belong to their engine.
    // Their cause class is compared separately; injected I/O failures and all
    // labels/order remain exact. This never alters file bytes or write decisions.
    for (const result of output.results) {
      const report = result.value as Report | undefined
      if (Array.isArray(report?.failures)) report.failures = report.failures.map(message => {
        if (scenario.nativeFailure === 'syntax') {
          expect(message).toMatch(/^providers\.json: (JSON Parse error:|unexpected end of JSON input$)/)
          return 'providers.json: <JSON syntax error>'
        }
        if (scenario.nativeFailure === 'base-url-type') {
          expect(message).toMatch(/^cc-haha\/settings\.json: baseUrl\.includes is not a function/)
          return 'cc-haha/settings.json: <baseUrl.includes TypeError>'
        }
        if (scenario.nativeFailure === 'model-type') {
          expect(['cc-haha/settings.json: No default value', 'cc-haha/settings.json: Cannot convert object to primitive value']).toContain(message)
          return 'cc-haha/settings.json: <model ToPrimitive TypeError>'
        }
        return message
      })
    }
    return output
  } finally { clearTimeout(timer); child.kill() }
}
async function persisted(home: string, scenario: Scenario) {
  const files: Array<{ path: string; bytes: string }> = []
  async function visit(directory: string, prefix: string) {
    const entries = await readdir(directory, { withFileTypes: true })
    for (const entry of entries.sort((a, b) => a.name.localeCompare(b.name))) {
      const path = join(directory, entry.name)
      const relative = prefix ? `${prefix}/${entry.name}` : entry.name
      if (entry.isDirectory()) await visit(path, relative)
      else if (entry.isFile()) files.push({ path: normalizeNonce(relative), bytes: (await readFile(path)).toString('hex') })
      else throw new Error('Migration fixture produced a non-regular entry')
    }
  }
  // Compare all selected configuration trees, including protected root/user
  // files. Bun's compiler cache under this disposable HOME is runtime output.
  for (const config of new Set(['.claude', ...(scenario.steps ?? []).map(step => step.config).filter((path): path is string => !!path)])) {
    try { await visit(join(home, config), config) } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    }
  }
  return files.sort((a, b) => a.path.localeCompare(b.path) || a.bytes.localeCompare(b.bytes))
}
async function snapshot(kind: 'typescript' | 'go', scenario: Scenario) {
  return withHome(async home => {
    await initialize(home, scenario)
    return { ...await run(kind, home, scenario), files: await persisted(home, scenario) }
  })
}
const provider = '{"id":"p","presetId":"custom","name":"Fixture","apiKey":"fake","baseUrl":"https://fixture.invalid","models":{"main":"m","haiku":"h","sonnet":"s","opus":"o"},"future":{"keep":true}}'
const legacy = '{"activeModel":"second","future":"original","providers":[{"id":"p","name":"Fixture","apiKey":"fake","baseUrl":"https://fixture.invalid","models":[{"id":"first"},{"id":"second"}],"notes":{"keep":true},"future":"source-only"}]}'
const scenarios: Array<[string, Scenario]> = [
  ['missing files leave no new state and cache result', {}],
  ['legacy root maps custom provider, active model, notes, and managed env', { files: { '.claude/providers.json': legacy, '.claude/settings.json': '{"sharedUnknown":true}' } }],
  ['legacy root missing credentials and no active provider do not invent managed env', { files: { '.claude/providers.json': '{"providers":[{"id":"p","name":"Fixture","apiKey":"","baseUrl":"b","models":[]}]}' } }],
  ['root malformed remains protected while owned settings continue', { nativeFailure: 'syntax', files: { '.claude/providers.json': '{"providers":', '.claude/cc-haha/settings.json': '{"env":null,"future":true}' } }],
  ['existing target prevents legacy copy and preserves v4 automatic compatibility', { files: { '.claude/providers.json': legacy, '.claude/cc-haha/providers.json': '{"schemaVersion":4,"activeId":"p","providers":[' + provider + ']}' } }],
  ['schema v3 filters invalid entries, resets toolSearch, preserves unknowns and order', { files: { '.claude/cc-haha/providers.json': '{"2":"two","1":"one","future":9007199254740993,"schemaVersion":3,"activeProviderId":"p","providers":[' + provider + ',{"invalid":true}],"providerOrder":["grok-official","p","p",4,"unknown"],"officialProviderModels":{"claude-official":{"main":"bad"},"future-slot":{"keep":true}},"surrogate":"\\ud800"}' } }],
  ['equal semantic shape with different key order still backs up and rewrites', { files: { '.claude/cc-haha/providers.json': '{"providerOrder":["claude-official","openai-official","grok-official"],"schemaVersion":6,"activeId":null,"officialProviderModels":{},"providers":[],"future":"keep"}' } }],
  ['current canonical index is read only despite original formatting', { files: { '.claude/cc-haha/providers.json': '{ "schemaVersion":6,"activeId":null,"providers":[],"providerOrder":["claude-official","openai-official","grok-official"],"officialProviderModels":{} }' } }],
  ['future schema and unknown compatibility preserve fields while source sets schema6', { files: { '.claude/cc-haha/providers.json': '{"schemaVersion":999,"activeId":"openai-official","providers":[' + provider.replace('"future":', '"requestCompatibility":{"futureBudget":true},"future":') + '],"future":true}' } }],
  ['valid OAuth mappings and future slots survive while malformed known entries are dropped', { files: { '.claude/cc-haha/providers.json': '{"schemaVersion":6,"providers":[],"officialProviderModels":{"claude-official":{"main":"m","haiku":"h","sonnet":"s","opus":"o","future":"keep"},"openai-official":{"main":"m","haiku":"h","sonnet":"s","opus":"o","fable":null},"grok-official":{"main":"m","haiku":"h","sonnet":"s","opus":"o","fable":""},"future-slot":null}}' } }],
  ['provider UTF16 identity distinguishes lone surrogate IDs and retains order strings', { files: { '.claude/cc-haha/providers.json': '{"schemaVersion":6,"activeId":"\\ud801","providers":[' + provider.replace('"id":"p"', '"id":"\\ud800"') + '],"providerOrder":["\\ud801","\\ud800","\\ud800","openai-official"]}' } }],
  ['overflow schema remains a JS number before stringify and duplicate keys keep first position', { files: { '.claude/cc-haha/providers.json': '{"z":0,"schemaVersion":1e400,"providers":[' + provider + '],"z":9007199254740993,"activeId":"grok-official","tiny":1e-400,"negative":-0,"\\ud800":"one","\\ud801":"two"}' } }],
  ['blank, BOM, and corrupt owned entries quarantine exact bytes then reset upgrades empty index', { files: { '.claude/cc-haha/providers.json': ' \n\t', '.claude/cc-haha/settings.json': '\ufeff{}' }, steps: [{ operation: 'ensure' }, { operation: 'ensure' }, { operation: 'reset' }, { operation: 'ensure' }] }],
  ['invalid UTF8 inside JSON replacement decodes without quarantine', { files: { '.claude/cc-haha/settings.json': Uint8Array.from([...Buffer.from('{"future":"'), 0xff, 0xfe, 0xe2, 0x82, ...Buffer.from('","env":null}')]) } }],
  ['DeepSeek legacy switch creates four capabilities and retains future env', { files: { '.claude/cc-haha/settings.json': '{"future":{"keep":true},"env":{"CC_HAHA_SEND_DISABLED_THINKING":"0","ANTHROPIC_BASE_URL":"https://api.deepseek.com/v1","KEEP":"yes"}}' } }],
  ['DeepSeek regexp supports array coercion and does not unicode fold Kelvin', { files: { '.claude/cc-haha/settings.json': '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_BASE_URL":[],"ANTHROPIC_MODEL":["deepseek-x"]}}' } }],
  ['non DeepSeek and falsy switch retain original bytes', { files: { '.claude/cc-haha/settings.json': '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":false,"ANTHROPIC_BASE_URL":7},"future":true}' } }],
  ['managed env runtime TypeError is a report failure and does not overwrite', { nativeFailure: 'base-url-type', files: { '.claude/cc-haha/settings.json': '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_BASE_URL":7}}' } }],
  ['model ToPrimitive error preserves source and continues report', { nativeFailure: 'model-type', files: { '.claude/cc-haha/settings.json': '{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":{"toString":null}}}' } }],
  ['actual settings read invokes real migration and shared user settings stay untouched', { files: { '.claude/providers.json': legacy, '.claude/settings.json': '{"futureUser":true}' }, steps: [{ operation: 'get-user' }, { operation: 'managed-read' }, { operation: 'ensure' }] }],
  ['last config cache reruns A after B and reset invalidates only cached selection', { files: { '.claude/cc-haha/settings.json': '{"env":null}', 'other-config/cc-haha/settings.json': '{"env":null}' }, steps: [{ operation: 'ensure' }, { operation: 'ensure' }, { operation: 'ensure', config: 'other-config' }, { operation: 'ensure', config: '.claude' }, { operation: 'reset' }, { operation: 'ensure' }] }],
]
for (const operation of ['access', 'read', 'copy', 'mkdir', 'write', 'rename', 'unlink']) {
  scenarios.push([`exact ${operation} failure ordering preserves reports and continues next target`, {
    files: { '.claude/cc-haha/providers.json': '{"providers":[]}', '.claude/cc-haha/settings.json': '{"env":null}' },
    faults: operation === 'unlink'
      ? [{ operation: 'rename', path: '.claude/cc-haha/providers.json.tmp.', message: 'fixture rename denied' }, { operation: 'unlink', path: '.claude/cc-haha/providers.json.tmp.', message: 'fixture cleanup denied' }]
      : [{ operation, path: operation === 'mkdir' ? '.claude/cc-haha' : '.claude/cc-haha/providers.json', message: `fixture ${operation} denied`, once: operation === 'mkdir' }],
  }])
}
scenarios.push(['strict quarantine failure never overwrites the corrupt source', { files: { '.claude/cc-haha/providers.json': '{', '.claude/cc-haha/settings.json': '{"env":null}' }, faults: [{ operation: 'rename', path: '.claude/cc-haha/providers.json', message: 'fixture quarantine denied' }] }])

for (const [name, scenario] of scenarios) {
  test(`PersistentStorage TS self replay and Go business parity: ${name}`, async () => {
    const expected = await snapshot('typescript', scenario)
    expect(await snapshot('typescript', scenario)).toEqual(expected)
    expect(await snapshot('go', scenario)).toEqual(expected)
  }, 40_000)
}

test('actual TS -> Go -> TS upgrades and managed settings retain unknowns and protected original files', async () => {
  await withHome(async home => {
    await initialize(home, { files: { '.claude/providers.json': legacy, '.claude/settings.json': '{"futureUser":{"keep":true}}', '.claude/cc-haha/settings.json': '{"futureManaged":{"keep":true},"env":null}' } })
    await run('typescript', home, { steps: [{ operation: 'ensure' }] })
    await run('go', home, { steps: [{ operation: 'managed-update', patch: { futureGo: { keep: true } } }, { operation: 'ensure' }] })
    const managed = (await run('typescript', home, { steps: [{ operation: 'managed-read' }, { operation: 'managed-update', patch: { futureTS: { keep: true } } }] })).results[0]!.value
    expect(managed).toMatchObject({ futureManaged: { keep: true }, futureGo: { keep: true }, env: {} })
    const final = (await run('go', home, { steps: [{ operation: 'managed-read' }] })).results[0]!.value
    expect(final).toMatchObject({ futureManaged: { keep: true }, futureGo: { keep: true }, futureTS: { keep: true } })
    expect(await readFile(join(home, '.claude/providers.json'), 'utf8')).toBe(legacy)
    expect(await readFile(join(home, '.claude/settings.json'), 'utf8')).toBe('{"futureUser":{"keep":true}}')
    expect(JSON.parse(await readFile(join(home, '.claude/cc-haha/providers.json'), 'utf8'))).toMatchObject({ schemaVersion: 6, activeId: 'p', providers: [{ notes: { keep: true } }] })
  })
}, 40_000)
