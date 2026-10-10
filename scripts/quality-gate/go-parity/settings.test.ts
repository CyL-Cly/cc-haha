import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const fixturePath = join(rootDir, 'scripts/quality-gate/go-parity/settings.fixture.ts')
let buildHome: string
let executable: string
const gitExecutable = Bun.which('git')
if (!gitExecutable) throw new Error('Actual git is required for offline SettingsService parity')

beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-settings-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'settings-fixture.exe' : 'settings-fixture')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/settings/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'),
    env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)

afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Step = { operation: string; patch?: Record<string, unknown>; unset?: string[]; root?: string; agent?: string; mode?: string; models?: { main: string; fable?: string; haiku: string; sonnet: string; opus: string } }
type Scenario = { project?: string; files?: Record<string, string>; shellEnv?: Record<string, string>; git?: boolean; steps: Step[] }
type Result = { value?: unknown; error?: { status: number; code: string; message: string } }
type Output = { results: Result[]; warningCount: number; migrationPaths: string[]; invalidations: number }

function environment(home: string) {
  return createSandboxedTestEnvironment(home, {
    CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(),
    GIT_CONFIG_GLOBAL: join(home, 'empty-global.gitconfig'),
    GIT_CONFIG_SYSTEM: join(home, 'empty-system.gitconfig'), GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0',
    PATH: `${dirname(gitExecutable!)}${process.platform === 'win32' ? ';' : ':'}${process.env.PATH ?? ''}`,
  })
}

async function withHome<T>(task: (home: string) => Promise<T>): Promise<T> {
  const home = await mkdtemp(join(tmpdir(), 'go-settings-parity-'))
  try { return await task(home) } finally { await rm(home, { recursive: true, force: true }) }
}

async function initialize(home: string, scenario: Scenario) {
  for (const [path, contents] of Object.entries(scenario.files ?? {})) {
    const absolute = join(home, path)
    await mkdir(dirname(absolute), { recursive: true })
    await writeFile(absolute, contents)
  }
  if (scenario.git) {
    const project = join(home, scenario.project ?? 'project')
    await mkdir(project, { recursive: true })
    const child = Bun.spawn([gitExecutable!, 'init', '-q', project], { cwd: home, env: environment(home), stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
    expect(code, stderr).toBe(0)
  }
}

async function run(kind: 'typescript' | 'go', home: string, scenario: Scenario): Promise<Output> {
  const steps = scenario.steps.map(step => ({ ...step, ...(step.root ? { root: join(home, step.root) } : {}) }))
  const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', fixturePath], {
    cwd: home, env: environment(home),
    stdin: new Blob([JSON.stringify({ homeDir: home, projectRoot: scenario.project ? join(home, scenario.project) : undefined, shellEnv: scenario.shellEnv ?? {}, gitExecutable, migrationScope: 'current-settings-fixture', steps })]),
    stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${kind} SettingsService fixture: ${stderr}`).toBe(0)
    return JSON.parse(stdout) as Output
  } finally { clearTimeout(timer); child.kill() }
}

async function persistedFiles(home: string, scenario: Scenario) {
  const directories = new Set(['.claude', '.claude/cc-haha', '.config/git'])
  for (const root of [scenario.project, ...scenario.steps.map(step => step.root)]) {
    if (root) directories.add(join(root, '.claude'))
  }
  const files: Record<string, unknown> = {}
  for (const directory of [...directories].sort()) {
    const absolute = join(home, directory)
    let names: string[]
    try { names = await readdir(absolute) } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') continue
      throw error
    }
    for (const name of names.sort()) {
      if (name === 'cc-haha') continue
      const text = await readFile(join(absolute, name), 'utf8')
      const path = relative(home, join(absolute, name)).replace(/\\/g, '/')
      if (name.includes('.invalid-')) files[path.replace(/\.invalid-\d+-[a-f0-9]+$/, '.invalid-<nonce>')] = { quarantinedBytes: text }
      else if (name.endsWith('.json')) {
        try { files[path] = JSON.parse(text) } catch { files[path] = { blankBytes: text } }
      } else files[path] = text
    }
  }
  return files
}

async function snapshot(kind: 'typescript' | 'go', scenario: Scenario) {
  return withHome(async home => {
    await initialize(home, scenario)
    const output = await run(kind, home, scenario)
    return { ...output, migrationPaths: output.migrationPaths.map(path => path.replace(/\\/g, '/')), files: await persistedFiles(home, scenario) }
  })
}

const modelMapping = { main: 'main-model', haiku: 'haiku-model', sonnet: 'sonnet-model', opus: 'opus-model' }
const scenarios: Array<[string, Scenario]> = [
  ['empty scopes and missing project errors', { steps: [{ operation: 'get-user' }, { operation: 'get-merged' }, { operation: 'get-project' }, { operation: 'get-local' }, { operation: 'update-project', patch: {} }, { operation: 'update-local', patch: {} }, { operation: 'get-teams' }, { operation: 'get-permission' }] }],
  ['user shallow patch and desktopTerminal future child preservation', { files: { '.claude/settings.json': '{"desktopTerminal":{"startupShell":"system","futureProfile":{"keep":true}},"env":{"OLD":"replaced"},"futureSetting":{"keep":true},"removeMe":1}' }, steps: [{ operation: 'update-user', patch: { theme: 'white', env: { NEW: 'only' }, desktopTerminal: { startupShell: 'pwsh' } }, unset: ['removeMe'] }, { operation: 'get-user' }, { operation: 'update-user', patch: { desktopTerminal: null } }, { operation: 'get-user' }] }],
  ['project and local scope selection and shallow merge', { project: 'project', steps: [{ operation: 'update-user', patch: { theme: 'dark', env: { user: 'keep' }, model: 'opus' } }, { operation: 'update-project', patch: { theme: 'light', env: { project: 'replace' } } }, { operation: 'update-local', patch: { theme: 'white', outputStyle: 'Learning' } }, { operation: 'get-merged' }, { operation: 'get-local' }, { operation: 'update-project', root: 'other-project', patch: { theme: 'other' } }, { operation: 'get-project', root: 'other-project' }] }],
  ['all permission modes and exact rejection', { files: { '.claude/settings.json': '{"defaultMode":"legacy-yolo","future":{"keep":true}}' }, steps: [{ operation: 'get-permission' }, ...['default', 'acceptEdits', 'plan', 'bypassPermissions', 'dontAsk', 'auto'].flatMap(mode => [{ operation: 'set-permission', mode }, { operation: 'get-permission' }]), { operation: 'set-permission', mode: 'invalid' }, { operation: 'get-user' }] }],
  ['team explicit preference validation and legacy env fallback', { files: { '.claude/settings.json': '{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0"},"future":{"keep":true}}' }, shellEnv: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '1' }, steps: [{ operation: 'get-teams' }, { operation: 'update-user', patch: { agentTeamsEnabled: true } }, { operation: 'get-teams' }, { operation: 'update-user', patch: { agentTeamsEnabled: false } }, { operation: 'get-teams' }, ...[null, 'true', 0, [], {}].map(value => ({ operation: 'update-user', patch: { agentTeamsEnabled: value } })), { operation: 'get-user' }] }],
  ['managed flag precedes user and shell, null falls through', { files: { '.claude/settings.json': '{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"1"}}', '.claude/cc-haha/settings.json': '{"env":{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS":"0","KEEP":"value"}}' }, shellEnv: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '0' }, steps: [{ operation: 'get-teams' }, { operation: 'managed-update', patch: { env: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: null } } }, { operation: 'get-teams' }, { operation: 'managed-update', patch: { env: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '' } } }, { operation: 'get-teams' }, { operation: 'managed-update', patch: { env: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: false } } }, { operation: 'get-teams' }] }],
  ['shell flag follows JS truthy and trim semantics', { shellEnv: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '\ufeffYES\ufeff' }, steps: [{ operation: 'get-teams' }] }],
  ['shell opt out', { shellEnv: { CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: '0' }, steps: [{ operation: 'get-teams' }] }],
  ['official model aliases preserve unrelated env and remove stale modelContext/Fable', { files: { '.claude/settings.json': '{"modelContext":200000,"env":{"USER_ENV":"keep","ANTHROPIC_DEFAULT_FABLE_MODEL":"old","futureEnv":{"keep":true}},"futureSetting":{"keep":true}}' }, steps: [{ operation: 'official-mapping', models: { ...modelMapping, fable: 'fable-model' } }, { operation: 'get-user' }, { operation: 'official-mapping', models: modelMapping }, { operation: 'get-user' }] }],
  ['agent overrides omitted/null/reset preserve future fields', { files: { '.claude/settings.json': '{"futureSetting":{"keep":true},"builtInAgentOverrides":{"Plan":{"model":"opus","futurePlan":"keep"},"Explore":{"model":"sonnet","effort":"high","futureAgent":{"keep":true}}}}' }, steps: [{ operation: 'override', agent: 'Explore', patch: { model: null, ignored: 'ignored' } }, { operation: 'get-user' }, { operation: 'override', agent: 'Plan', patch: { effort: 7 } }, { operation: 'override', agent: 'General', patch: { model: 'haiku' } }, { operation: 'override', agent: 'General', patch: { model: null } }, { operation: 'reset-override', agent: 'Explore' }, { operation: 'reset-override', agent: 'absent' }, { operation: 'get-user' }] }],
  ['last agent override removes empty containers', { steps: [{ operation: 'override', agent: 'Explore', patch: { model: 'haiku', effort: 'low' } }, { operation: 'override', agent: 'Explore', patch: { model: null } }, { operation: 'get-user' }, { operation: 'reset-override', agent: 'Explore' }, { operation: 'get-user' }] }],
  ['managed updater result and env replacement stay isolated from user settings', { files: { '.claude/settings.json': '{"userFuture":{"keep":true},"env":{"ANTHROPIC_API_KEY":"fake-user-key"}}', '.claude/cc-haha/settings.json': '{"customField":"keep","modelContext":200000,"env":{"EXISTING_VAR":"keep","ANTHROPIC_API_KEY":"fake-managed-key"}}' }, steps: [{ operation: 'managed-read' }, { operation: 'managed-update', patch: { env: { REPLACEMENT: 'only' }, model: 'fixture-model' }, unset: ['modelContext'] }, { operation: 'managed-read' }, { operation: 'get-user' }] }],
  ['corrupt user settings quarantine original before update', { files: { '.claude/settings.json': '{"env":' }, steps: [{ operation: 'get-user' }, { operation: 'update-user', patch: { theme: 'dark' } }, { operation: 'get-user' }] }],
  ['blank user settings remain untouched on read', { files: { '.claude/settings.json': ' \n\t' }, steps: [{ operation: 'get-user' }, { operation: 'get-teams' }] }],
  ['actual local save writes injected global gitignore', { project: 'project', git: true, steps: [{ operation: 'update-local', patch: { outputStyle: 'Learning' } }, { operation: 'get-local' }] }],
  ['actual local save respects repository gitignore', { project: 'project', git: true, files: { 'project/.gitignore': '.claude/settings.local.json\n' }, steps: [{ operation: 'update-local', patch: { outputStyle: 'Learning' } }, { operation: 'get-local' }] }],
]

for (const [name, scenario] of scenarios) {
  test(`SettingsService TS self replay and Go business parity: ${name}`, async () => {
    const expected = await snapshot('typescript', scenario)
    expect(await snapshot('typescript', scenario)).toEqual(expected)
    expect(await snapshot('go', scenario)).toEqual(expected)
  }, 40_000)
}

test('actual TS -> Go -> TS business settings writes retain future fields and old agent overrides', async () => {
  await withHome(async home => {
    await initialize(home, { files: { '.claude/settings.json': '{"future":{"keep":true},"desktopTerminal":{"startupShell":"system","futureProfile":{"keep":true}},"builtInAgentOverrides":{"Plan":{"model":"opus","futureAgent":"keep"}}}' }, steps: [] })
    await run('typescript', home, { steps: [{ operation: 'update-user', patch: { language: '中文 😀', desktopTerminal: { startupShell: 'pwsh' } } }] })
    await run('go', home, { steps: [{ operation: 'override', agent: 'Plan', patch: { effort: 'high' } }, { operation: 'official-mapping', models: modelMapping }] })
    const current = (await run('typescript', home, { steps: [{ operation: 'get-user' }] })).results[0]!.value as Record<string, unknown>
    expect(current).toMatchObject({ future: { keep: true }, desktopTerminal: { startupShell: 'pwsh', futureProfile: { keep: true } }, builtInAgentOverrides: { Plan: { model: 'opus', effort: 'high', futureAgent: 'keep' } }, language: '中文 😀', model: 'main-model' })
    await run('typescript', home, { steps: [{ operation: 'reset-override', agent: 'Plan' }, { operation: 'set-permission', mode: 'auto' }] })
    const after = (await run('go', home, { steps: [{ operation: 'get-user' }] })).results[0]!.value as Record<string, unknown>
    expect(after).toMatchObject({ future: { keep: true }, builtInAgentOverrides: { Plan: { futureAgent: 'keep' } }, defaultMode: 'auto', model: 'main-model' })
    expect(after.schemaVersion).toBeUndefined()
  })
}, 40_000)
