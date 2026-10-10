import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { basename, join, resolve } from 'node:path'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { globalConfigProfiles, type GlobalConfigProfile } from './cli-global-config-fixtures'

const root = resolve(import.meta.dir, '../../..')
const sourceOnly = process.env.CC_HAHA_GLOBAL_CONFIG_SOURCE_ONLY === '1'
let buildHome: string, executable: string
beforeAll(async () => {
  if (sourceOnly) return
  buildHome = await mkdtemp(join(tmpdir(), 'go-cli-global-config-build-'))
  executable = join(buildHome, 'fixture.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/cliglobalconfig'], { cwd: join(root, 'backend'), env: await createGoBuildEnvironment(root, buildHome), stdout: 'pipe', stderr: 'pipe', stdin: 'ignore' })
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
  expect(code, stdout + stderr).toBe(0)
}, 180000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

async function run(source: boolean, profile: GlobalConfigProfile) {
  const home = await mkdtemp(join(tmpdir(), 'cli-global-config-parity-'))
  try {
    const env = createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CC_HAHA_GLOBAL_CONFIG_FIXTURE: '1', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', ANTHROPIC_API_KEY: 'fixture-not-a-secret' })
    const child = Bun.spawn(source ? [process.execPath, '--no-env-file', join(root, 'scripts/quality-gate/go-parity/cli-global-config.fixture.ts')] : [executable, '-test.run=^TestFixture$'], { cwd: home, env, stdin: new Blob([JSON.stringify({ ...profile, home })]), stdout: 'pipe', stderr: 'pipe' })
    const timer = setTimeout(() => child.kill(), 20000)
    try {
      const [code, stdoutBytes, stderrBytes] = await Promise.all([child.exited, new Response(child.stdout).arrayBuffer(), new Response(child.stderr).arrayBuffer()])
      const stdout = Buffer.from(stdoutBytes).toString('utf8')
      const stderr = Buffer.from(stderrBytes).toString('utf8')
      // Raw evidence is independent of the comparison projection: its literal
      // base64 fields and allocated HOME bytes are retained before any rewrite.
      const evidenceDir = join(root, 'artifacts/go-parity/cli-global-config-raw')
      await mkdir(evidenceDir, { recursive: true })
      await writeFile(join(evidenceDir, basename(home) + '-' + (source ? 'source' : 'go') + '.json'), JSON.stringify({ language: source ? 'source' : 'go', profile: profile.name, allocationRoot: home, code, stdoutBase64: Buffer.from(stdoutBytes).toString('base64'), stderrBase64: Buffer.from(stderrBytes).toString('base64') }, null, 2) + '\n')
      expect(code, stdout + stderr).toBe(0)
      const result = JSON.parse(stdout.replace(/\nPASS\n$/, '\n').replaceAll(home, '<HOME>').trim())
      // The same declared allocation-root metadata is embedded in project
      // file bytes. Decode/rewrite/re-encode only that exact HOME sequence;
      // replacement-equivalent invalid UTF-8 byte differences remain visible.
      for (const step of result.results) for (const file of Object.values(step.files) as any[]) {
        if (file.rawBase64 !== undefined) file.rawBase64 = Buffer.from(Buffer.from(file.rawBase64, 'base64').toString('latin1').replaceAll(Buffer.from(home).toString('latin1'), '<HOME>'), 'latin1').toString('base64')
      }
      if (source) expect(result.capabilities.blocked).toEqual([])
      return { observation: { path: result.path, results: result.results, effects: result.effects }, stderr: stderr.replaceAll(home, '<HOME>') }
    } finally { clearTimeout(timer) }
  } finally { await rm(home, { recursive: true, force: true }) }
}

for (const profile of globalConfigProfiles) {
  test('actual production global config Source self replay: ' + profile.name, async () => {
    expect(await run(true, profile)).toEqual(await run(true, profile))
  }, 60000)
  ;(sourceOnly ? test.skip : test)('actual production global config Go parity: ' + profile.name, async () => {
    const reference = await run(true, profile)
    expect(await run(false, profile)).toEqual(reference)
  }, 60000)
}

test.each(['file bytes', 'raw bytes', 'updater count', 'auth loss', 'unknown UTF16 value', 'effect order', 'diagnostic order'])('strict global config comparator rejects semantic mutant: %s', async mutation => {
  const name = mutation === 'raw bytes' ? 'corrupt-byte-backups-deduplicate-decoded-utf8' : mutation === 'diagnostic order' ? 'strict-corrupt-enable-before-recovery' : mutation === 'auth loss' ? 'auth-object-corrupt-reread' : mutation === 'unknown UTF16 value' ? 'unknown-order-utf16-numbers' : 'throw-once-fallback-updater-twice'
  const reference = await run(true, globalConfigProfiles.find(profile => profile.name === name)!)
  const mutant = structuredClone(reference)
  if (mutation === 'file bytes') {
    const files = mutant.observation.results.at(-1)!.files
    files['.claude/.claude.json'].bytes += '\n'
  } else if (mutation === 'raw bytes') {
    const file = mutant.observation.results.at(-1)!.files['.claude/.claude.json']
    const raw = Buffer.from(file.rawBase64, 'base64')
    raw[raw.length - 1] = 128 // Same decoded �; different retained byte.
    file.rawBase64 = raw.toString('base64')
  } else if (mutation === 'updater count') {
    mutant.observation.results.find((result: any) => result.operation === 'save').calls = 1
  } else if (mutation === 'auth loss') {
    mutant.observation.results.find((result: any) => result.operation === 'save').calls = 1
  } else if (mutation === 'unknown UTF16 value') {
    const result = mutant.observation.results.find((result: any) => result.value?.includes('\\ud800'))
    result.value = result.value.replace('\\ud800', '�')
  } else if (mutation === 'effect order') {
    const effects = mutant.observation.effects
    const updater = effects.findIndex((effect: any) => effect.kind === 'updater')
    const write = effects.findIndex((effect: any) => effect.kind === 'writeFlush')
    ;[effects[updater], effects[write]] = [effects[write], effects[updater]]
  } else {
    const lines = mutant.stderr.split('\n')
    ;[lines[1], lines[2]] = [lines[2], lines[1]]
    mutant.stderr = lines.join('\n')
  }
  expect(() => expect(mutant).toEqual(reference)).toThrow()
}, 30000)

test.each(['home', 'config', 'cwd'])('global config guard rejects invalid %s before production imports', async boundary => {
  const home = await mkdtemp(join(tmpdir(), 'cli-global-config-guard-'))
  try {
    const loader = join(home, 'guard.ts')
    await writeFile(loader, `import { plugin } from 'bun'\nplugin({ name: 'tripwire', setup(build) { build.onLoad({ filter: /[\\\\/]src[\\\\/].*\\.(?:ts|tsx|js)$/ }, () => { process.stdout.write('PRODUCTION_REACHED\\n'); return { contents: 'throw new Error("production reached")', loader: 'ts' } }) } })\nawait import(${JSON.stringify(join(root, 'scripts/quality-gate/go-parity/cli-global-config.fixture.ts'))})`)
    const child = Bun.spawn([process.execPath, '--no-env-file', loader], { cwd: boundary === 'cwd' ? root : home, env: createSandboxedTestEnvironment(home, { CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), ...(boundary === 'home' ? { HOME: join(home, 'wrong') } : {}), ...(boundary === 'config' ? { CLAUDE_CONFIG_DIR: join(home, 'wrong') } : {}) }), stdin: new Blob([JSON.stringify({ name: 'guard', home, steps: [] })]), stdout: 'pipe', stderr: 'pipe' })
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code).not.toBe(0)
    expect(stdout).not.toContain('PRODUCTION_REACHED')
    expect(stderr).toContain('fixture requires disposable HOME/config before production imports')
  } finally { await rm(home, { recursive: true, force: true }) }
})

// Golden capture is a separate explicitly requested evidence operation; test
// execution never rewrites a Source observation.
if (process.env.CC_HAHA_CAPTURE_GLOBAL_CONFIG === '1') {
  const observations = []
  for (const profile of globalConfigProfiles) observations.push({ input: profile, expected: await run(true, profile) })
  await writeFile(join(root, 'backend/internal/cliglobalconfig/testdata/source_contract.json'), JSON.stringify(observations, null, 2) + '\n')
}
