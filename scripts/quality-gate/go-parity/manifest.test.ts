import { expect, test } from 'bun:test'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createHermeticGoEnvironment } from '../../pr/go-dependencies'
import { assertCorpusSnapshotFresh, collectCorpusSnapshot, runCorpusWithManifest } from './manifest'

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'go-corpus-snapshot-test-'))
  mkdirSync(join(root, 'backend'), { recursive: true })
  writeFileSync(join(root, 'backend/query.go'), 'package fixture\n')
  writeFileSync(join(root, 'backend/spec.json'), '{"text":"before"}')
  writeFileSync(join(root, 'oracle.test.ts'), 'export {}\n')
  writeFileSync(join(root, 'bun.lock'), 'fixture lock\n')
  return root
}

test.each(['resource', 'oracle', 'added', 'deleted', 'runtime', 'test-failure', 'success'] as const)('corpus endpoint evidence detects %s independently of child exit success', async scenario => {
  const root = fixture()
  let versions = 0
  try {
    const report = await runCorpusWithManifest({
      rootDir: root, artifactDir: join(root, 'artifacts/checkpoint'), corpus: [join(root, 'oracle.test.ts')], sourceEnvironment: { PATH: '/fixture', ANTHROPIC_API_KEY: 'must-not-leak' },
      execute: async (command, options) => {
        expect(options.env.HOME).not.toBe(process.env.HOME)
        expect(options.env.ANTHROPIC_API_KEY).toBeUndefined()
        expect(options.env.GOPROXY).toBe('off')
        if (command.includes('--version')) return { exitCode: 0, stdout: '1.3.14\n', stderr: '' }
        if (command[0] === 'go') return { exitCode: 0, stdout: `go version ${scenario === 'runtime' && versions++ ? 'go1.25.8' : 'go1.25.7'} darwin/arm64\n`, stderr: '' }
        if (command[0] === 'git') return { exitCode: 0, stdout: command.includes('--deleted') ? (scenario === 'deleted' && versions ? 'bun.lock\0' : '') : ['backend/query.go', 'backend/spec.json', 'oracle.test.ts', 'bun.lock', ...(scenario === 'added' && versions ? ['new.json'] : [])].join('\0') + '\0', stderr: '' }
        expect(command.slice(1, 3)).toEqual(['--no-env-file', 'test'])
        if (scenario === 'resource') writeFileSync(join(root, 'backend/spec.json'), '{"text":"after"}')
        if (scenario === 'oracle') writeFileSync(join(root, 'oracle.test.ts'), 'export const changed = true\n')
        if (scenario === 'added') writeFileSync(join(root, 'new.json'), '{}')
        if (scenario === 'deleted') rmSync(join(root, 'bun.lock'))
        if (scenario !== 'runtime') versions++
        return { exitCode: scenario === 'test-failure' ? 1 : 0, stdout: '', stderr: '' }
      },
    })
    expect(report.status).toBe(scenario === 'success' ? 'passed' : 'failed')
    expect(report.corpus).toEqual(['oracle.test.ts'])
    expect(report.runtime.bun).toBe('1.3.14')
    expect(report.before.map(entry => entry.file)).toContain('backend/spec.json')
    if (['resource', 'oracle', 'added', 'deleted'].includes(scenario)) expect(report.failures[0]).toContain('Corpus inputs changed')
    expect(JSON.parse(readFileSync(join(root, 'artifacts/checkpoint/manifest.json'), 'utf8')).status).toBe(report.status)
  } finally { rmSync(root, { recursive: true, force: true }) }
})

test.each([false, true])('actual offline Bun child records a manifest and rejects resource mutation: %s', async mutate => {
  const root = fixture()
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-real-child-home-'))
  try {
    const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
    const child = Bun.spawn(['git', 'init', '-q', root], { env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    expect(await child.exited, await new Response(child.stderr).text()).toBe(0)
    writeFileSync(join(root, '.gitignore'), 'artifacts/\n')
    writeFileSync(join(root, 'oracle.test.ts'), `import { test, expect } from 'bun:test'\nimport { writeFileSync } from 'node:fs'\ntest('disposable metadata probe', () => { ${mutate ? "writeFileSync('backend/spec.json', '{}');" : ''} expect(1).toBe(1) })\n`)
    const report = await runCorpusWithManifest({ rootDir: root, artifactDir: join(root, 'artifacts/checkpoint'), corpus: [join(root, 'oracle.test.ts')], sourceEnvironment: { PATH: process.env.PATH } })
    expect(report.childExitCode).toBe(0)
    expect(report.status).toBe(mutate ? 'failed' : 'passed')
    expect(report.runtime.goBefore).toMatch(/^go version go1\.25\.7 /)
    if (mutate) expect(report.failures[0]).toContain('backend/spec.json')
  } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
})

test('actual Git inventory includes untracked inputs and excludes private ignored outputs; resource escape fails closed', async () => {
  const root = fixture()
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-inventory-home-'))
  try {
    const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
    const child = Bun.spawn(['git', 'init', '-q', root], { env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
    expect(await child.exited, await new Response(child.stderr).text()).toBe(0)
    writeFileSync(join(root, '.gitignore'), 'artifacts/\n.env\n')
    mkdirSync(join(root, 'artifacts'))
    writeFileSync(join(root, 'artifacts/generated.json'), 'must-not-enter')
    writeFileSync(join(root, '.env'), 'FAKE_SECRET=must-not-enter')
    const snapshot = await collectCorpusSnapshot(root, env)
    expect(snapshot.map(entry => entry.file)).toEqual(['.gitignore', 'backend/query.go', 'backend/spec.json', 'bun.lock', 'oracle.test.ts'])
    symlinkSync(join(home, 'outside.json'), join(root, 'escape.json'))
    await expect(collectCorpusSnapshot(root, env)).rejects.toThrow('symlink')
  } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
})

test('an unlisted corpus file or escaping Git inventory cannot become passing evidence', async () => {
  const root = fixture()
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-invalid-inventory-'))
  try {
    let started = false
    const report = await runCorpusWithManifest({
      rootDir: root, artifactDir: join(root, 'artifacts/checkpoint'), corpus: [join(root, 'oracle.test.ts')], sourceEnvironment: { PATH: process.env.PATH },
      execute: async command => {
        if (command.includes('--version')) return { exitCode: 0, stdout: '1.3.14\n', stderr: '' }
        if (command[0] === 'go') return { exitCode: 0, stdout: 'go version go1.25.7 darwin/arm64', stderr: '' }
        if (command[0] === 'git') return { exitCode: 0, stdout: command.includes('--deleted') ? '' : 'backend/spec.json\0', stderr: '' }
        started = true
        return { exitCode: 0, stdout: '', stderr: '' }
      },
    })
    expect(report.status).toBe('failed')
    expect(report.failures[0]).toContain('missing from the repository input inventory')
    expect(started).toBe(false)
    await expect(collectCorpusSnapshot(root, createHermeticGoEnvironment(home, { PATH: process.env.PATH }), async command => ({ exitCode: 0, stdout: command.includes('--deleted') ? '' : '../outside.json\0', stderr: '' }))).rejects.toThrow('Invalid corpus input path')
  } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
})

test('actual Git tracked deletions leave current inputs and restoration invalidates freshness', async () => {
  const root = fixture()
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-deletion-home-'))
  try {
    const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
    for (const args of [['init', '-q'], ['add', 'backend/spec.json', 'oracle.test.ts']]) {
      const child = Bun.spawn(['git', ...args], { cwd: root, env, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
      const [exitCode, stderr] = await Promise.all([child.exited, new Response(child.stderr).text()])
      expect(exitCode, stderr).toBe(0)
    }
    const original = await collectCorpusSnapshot(root, env)
    rmSync(join(root, 'backend/spec.json'))
    const deleted = await collectCorpusSnapshot(root, env)
    expect(deleted.map(entry => entry.file)).not.toContain('backend/spec.json')
    expect(() => assertCorpusSnapshotFresh(original, deleted)).toThrow('backend/spec.json')
    writeFileSync(join(root, 'backend/spec.json'), '{"text":"before"}')
    const restored = await collectCorpusSnapshot(root, env)
    expect(restored).toEqual(original)
    expect(() => assertCorpusSnapshotFresh(deleted, restored)).toThrow('backend/spec.json')
  } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
})

test('deletion metadata cannot hide restored inputs, symlinks, invalid paths or an unavailable inventory', async () => {
  const root = fixture()
  const home = mkdtempSync(join(tmpdir(), 'go-corpus-deletion-guard-'))
  const env = createHermeticGoEnvironment(home, { PATH: process.env.PATH })
  const run = (names: string[], deleted: string[], deletionCode = 0) => async (command: string[]) => ({
    exitCode: command.includes('--deleted') ? deletionCode : 0,
    stdout: (command.includes('--deleted') ? deleted : names).join('\0') + '\0', stderr: '',
  })
  try {
    const snapshot = await collectCorpusSnapshot(root, env, run(['backend/spec.json'], ['backend/spec.json']))
    expect(snapshot[0]?.file).toBe('backend/spec.json')
    symlinkSync(join(home, 'absent.json'), join(root, 'escape.json'))
    await expect(collectCorpusSnapshot(root, env, run(['escape.json'], ['escape.json']))).rejects.toThrow('symlink')
    await expect(collectCorpusSnapshot(root, env, run(['../missing.json'], ['../missing.json']))).rejects.toThrow('Invalid corpus input path')
    await expect(collectCorpusSnapshot(root, env, run(['backend/spec.json'], ['missing.json']))).rejects.toThrow('missing from Git')
    await expect(collectCorpusSnapshot(root, env, run(['backend/spec.json'], [], 1))).rejects.toThrow('Could not inventory deleted corpus inputs')
    rmSync(join(root, 'backend/spec.json'))
    await expect(collectCorpusSnapshot(root, env, run(['backend/spec.json'], []))).rejects.toThrow('ENOENT')
  } finally { rmSync(root, { recursive: true, force: true }); rmSync(home, { recursive: true, force: true }) }
})
