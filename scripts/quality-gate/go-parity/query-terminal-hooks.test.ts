import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { isAbsolute, join } from 'node:path'
import { collectCorpusSnapshot, assertCorpusSnapshotFresh } from './manifest'
import { createGoBuildEnvironment, parseGoJsonStream } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { hookAssistantSource, hookHistorySources, queryTerminalHookProfiles, type HookProfile } from './query-terminal-hooks-fixtures'

const root = join(import.meta.dir, '../../..')
const sourceOnly = process.env.CC_HAHA_HOOK_SOURCE_ONLY === '1'
const artifact = join(root, 'artifacts/go-parity/query-terminal-hooks')
let buildHome: string, executable: string
let inputSnapshot: Awaited<ReturnType<typeof collectCorpusSnapshot>>, snapshotEnvironment: Record<string, string>, selectedInputs = new Set<string>()
let goVersion = ''
async function capture(command: string[]) {
  const child = Bun.spawn(command, { cwd: join(root, 'backend'), env: snapshotEnvironment, stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
  return stdout
}
async function compiledInputs() {
  const paths = new Set<string>()
  for (const pkg of parseGoJsonStream(await capture(['go', 'list', '-json', '-deps', '-test', './internal/queryhooks']))) {
    if (pkg.Module?.Path !== 'github.com/NanmiCoder/cc-haha/backend') continue
    const fields = ['GoFiles', 'EmbedFiles', ...(pkg.ImportPath === 'github.com/NanmiCoder/cc-haha/backend/internal/queryhooks' ? ['TestGoFiles', 'XTestGoFiles'] : [])]
    for (const field of fields) for (const file of pkg[field] ?? []) {
      const path = isAbsolute(file) ? file : join(pkg.Dir, file)
      if (isAbsolute(file) && path.startsWith(snapshotEnvironment.GOCACHE + '/')) continue
      if (!path.startsWith(root + '/backend/')) throw new Error('Unknown compiled source path: ' + path)
      paths.add(path.slice(root.length + 1))
    }
  }
  return paths
}
const sourceInput = (file: string) => file.startsWith('src/') || file.startsWith('scripts/quality-gate/go-parity/query-terminal-hooks') || file.startsWith('backend/internal/queryhooks/') || ['scripts/quality-gate/go-parity/manifest.ts', 'scripts/quality-gate/go-parity/client-options-capabilities.ts', 'scripts/quality-gate/go-parity/fixture-guard.ts', 'scripts/pr/test-environment.ts', 'scripts/pr/go-dependencies.ts', 'backend/go.mod', 'backend/go.sum', 'package.json', 'bun.lock'].includes(file)
beforeAll(async () => {
  if (Bun.version !== '1.3.14') throw new Error('Pinned Bun1.3.14 required')
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(), 'query-hooks-build-'))
  executable = join(buildHome, 'fixture.test')
  snapshotEnvironment = await createGoBuildEnvironment(root, buildHome)
  goVersion = (await capture(['go', 'version'])).trim()
  expect(goVersion).toMatch(/^go version go1\.25\.7 /)
  selectedInputs = await compiledInputs()
  inputSnapshot = await collectCorpusSnapshot(root, snapshotEnvironment)
  selectedInputs = new Set([...selectedInputs, ...inputSnapshot.filter(({ file }) => sourceInput(file)).map(({ file }) => file)])
  const child = Bun.spawn(['go', 'test', '-race', '-c', '-o', executable, './internal/queryhooks'], { cwd: join(root, 'backend'), env: snapshotEnvironment, stdout: 'pipe', stderr: 'pipe' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => {
  if (!buildHome) return
  try {
    if (inputSnapshot) {
      const after = await collectCorpusSnapshot(root, snapshotEnvironment)
      selectedInputs = new Set([...selectedInputs, ...await compiledInputs(), ...after.filter(({ file }) => sourceInput(file)).map(({ file }) => file)])
      const versionAfter = (await capture(['go', 'version'])).trim()
      const beforeSelected = inputSnapshot.filter(({ file }) => selectedInputs.has(file)), afterSelected = after.filter(({ file }) => selectedInputs.has(file))
      const beforeHashes = new Map(inputSnapshot.map(value => [value.file, value.sha256]))
      const afterHashes = new Map(after.map(value => [value.file, value.sha256]))
      const changed = [...new Set([...beforeHashes.keys(), ...afterHashes.keys()])].filter(file => beforeHashes.get(file) !== afterHashes.get(file))
      await mkdir(artifact, { recursive: true })
      await writeFile(join(artifact, 'focused-manifest.json'), JSON.stringify({ scope: 'compiled Go closure/raw golden/embed plus all Source TS and fixture/helpers/locks', versions: { bun: Bun.version, revision: Bun.revision, go: goVersion }, profiles: { total: 28, strict: 25, activePending: 3 }, before: beforeSelected, after: afterSelected, repositoryBefore: inputSnapshot, repositoryAfter: after, contextOnlyRepositoryChanges: changed.filter(file => !selectedInputs.has(file)), limitations: ['Endpoint hashes do not catch mutate-and-restore events.', 'Installed dependency bytes rely on frozen installation and hashed locks.', 'Source selection conservatively includes all src files; this is not an observed import-only graph.'] }, null, 2))
      assertCorpusSnapshotFresh(beforeSelected, afterSelected)
      expect(versionAfter).toBe(goVersion)
    }
  } finally { await rm(buildHome, { recursive: true, force: true }) }
})
async function observe(profile: HookProfile, source: boolean) {
  const home = await mkdtemp(join(tmpdir(), 'query-hooks-original-'))
  try {
    await mkdir(join(home, '.claude'), { recursive: true })
    const input = { ...profile, home, historySources: hookHistorySources, assistantSource: hookAssistantSource }
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_QUERY_TERMINAL_HOOKS: '1', NODE_ENV: 'production', USER_TYPE: 'external', CLAUDE_CODE_ENABLE_XAA: '0', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: '0', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', ANTHROPIC_API_KEY: 'offline-original-key' })
    const command = source ? [process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(root, 'scripts/quality-gate/go-parity/query-terminal-hooks.fixture.ts')] : [executable, '-test.run=^TestQueryTerminalHooksFixture$']
    const child = Bun.spawn(command, { cwd: home, env, stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const timeout = setTimeout(() => child.kill(), 30000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      await mkdir(artifact, { recursive: true }); await writeFile(join(artifact, profile.name + '-' + (source ? 'source' : 'go') + '-' + Date.now() + '.json'), JSON.stringify({ original: input, command, code, stdout, stderr }, null, 2))
      expect(code, stdout + stderr).toBe(0); expect(stderr).toBe('')
      const parsed = JSON.parse(source ? stdout : stdout.split('QUERY_HOOKS_RESULT:')[1].split('\n')[0])
      if (source) { expect(parsed.sourceOnly.capabilities.blocked).toEqual([]); expect(parsed.sourceOnly.capabilities.secureReads).toEqual([]) }
      return { name: parsed.name, results: parsed.results }
    } finally { clearTimeout(timeout) }
  } finally { await rm(home, { recursive: true, force: true }) }
}
for (const profile of queryTerminalHookProfiles) {
  test('query hook actual Source self: ' + profile.name, async () => { expect(await observe(profile, true)).toEqual(await observe(profile, true)) }, 60000)
  ;(sourceOnly ? test.skip : test)('query hook independent original Go: ' + profile.name, async () => {
    const source = await observe(profile, true), candidate = await observe(profile, false)
    if (profile.supported === false) {
      expect(candidate.results.at(-1).capability).toBe(true)
      // Active Source execution is kept as pending evidence, not equivalent.
      expect(source.results.at(-1).phases.some((phase: string) => phase.startsWith('active-'))).toBe(true)
    } else expect(candidate).toEqual(source)
  }, 60000)
}
test('query hook comparator catches lifecycle, identity, allocation and cache omissions', async () => {
  const expected = await observe(queryTerminalHookProfiles[4], true)
  for (const mutate of [(value: any) => value.results[0].value.raw = '{}', (value: any) => value.results[2].value.identity++, (value: any) => value.results.at(-1).value.assistantIdentity = false, (value: any) => value.results.at(-2).uuidCalls++, (value: any) => value.results.at(-2).phases.reverse()]) {
    const changed = structuredClone(expected); mutate(changed); expect(() => expect(changed).toEqual(expected)).toThrow()
  }
}, 30000)
for (const fault of ['home', 'config', 'prepared', 'operation'] as const) test('query hooks guard before production: ' + fault, async () => {
  const home = await mkdtemp(join(tmpdir(), 'query-hooks-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import {plugin} from 'bun'\nplugin({name:'tripwire',setup(build){build.onLoad({filter:/[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/},()=>{process.stdout.write('PRODUCTION_REACHED');return {contents:'throw new Error("production reached")',loader:'ts'}})}})\nawait import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/query-terminal-hooks.fixture.ts'))})`)
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), CC_HAHA_QUERY_TERMINAL_HOOKS: '1', NODE_ENV: 'production', ...(fault === 'home' ? { HOME: home + '/escape' } : {}), ...(fault === 'config' ? { CLAUDE_CONFIG_DIR: home + '/escape' } : {}) })
    const input: any = { home, name: 'guard', operations: [], historySources: hookHistorySources, assistantSource: hookAssistantSource }
    if (fault === 'prepared') input.preparedSnapshot = {}
    if (fault === 'operation') input.operations = [{ kind: 'wire' }]
    const child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', loader], { cwd: home, env, stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]); expect(code).not.toBe(0); expect(stdout).not.toContain('PRODUCTION_REACHED'); expect(stderr).toContain(fault === 'home' || fault === 'config' ? 'disposable HOME/config' : 'before imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})
if (process.env.CC_HAHA_CAPTURE_QUERY_HOOKS === '1') test('capture independently self-proved Source contract for native regression', async () => {
  const rows = []
  for (const profile of queryTerminalHookProfiles.filter(profile => profile.supported !== false)) { const first = await observe(profile, true); expect(await observe(profile, true)).toEqual(first); rows.push({ input: { ...profile, historySources: hookHistorySources, assistantSource: hookAssistantSource }, expected: first }) }
  await mkdir(join(root, 'backend/internal/queryhooks/testdata'), { recursive: true }); await writeFile(join(root, 'backend/internal/queryhooks/testdata/source_contract.json'), JSON.stringify(rows, null, 2) + '\n')
}, 180000)
