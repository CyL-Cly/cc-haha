import { afterAll, beforeAll, describe, expect, test } from 'bun:test'
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { defaultH5Interfaces, h5ServiceScenarios, protectedUserSettings, type H5ServiceOutput, type H5ServiceRequest, type H5ServiceScenario } from './h5-service-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
const fixturePath = join(rootDir, 'scripts/quality-gate/go-parity/h5-service.fixture.ts')
let executable: string

async function initialize(home: string, scenario: H5ServiceScenario) {
  for (const [path, text] of Object.entries({ '.claude/settings.json': protectedUserSettings, ...scenario.files })) {
    if (isAbsolute(path) || path.split(/[\\/]/).includes('..')) throw new Error(`Unsafe H5 fixture relative path: ${path}`)
    const absolute = join(home, path)
    await mkdir(dirname(absolute), { recursive: true })
    await writeFile(absolute, text)
  }
}

/** Observe actual files independently of either fixture's reported snapshots. */
async function persistedFiles(home: string) {
  const files: Record<string, unknown> = {}
  async function walk(directory: string): Promise<void> {
    let entries: import('node:fs').Dirent[]
    try { entries = await readdir(directory, { withFileTypes: true }) } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') return
      throw error
    }
    for (const entry of entries.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0)) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) await walk(path)
      else {
        const text = await readFile(path, 'utf8')
        const name = relative(home, path).replace(/\\/g, '/')
        try { files[name] = { json: JSON.parse(text), source: canonicalJsonSource(text) } } catch { files[name] = { raw: text } }
      }
    }
  }
  await walk(join(home, '.claude'))
  return files
}

async function record(kind: 'typescript' | 'go', scenario: H5ServiceScenario): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'h5-service-parity-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await initialize(home, scenario)
    const input: H5ServiceRequest = { homeDir: home, interfaces: scenario.interfaces ?? defaultH5Interfaces, activePort: scenario.activePort ?? 28670, environment: scenario.environment ?? {}, steps: scenario.steps }
    child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', fixturePath], {
      cwd: home, env: createSandboxedTestEnvironment(home), stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 15_000)
    try {
      const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(exitCode, `${kind} H5AccessService fixture: ${stderr}`).toBe(0)
      const output = JSON.parse(stdout) as H5ServiceOutput
      expect(output.results).toHaveLength(scenario.steps.length)
      expect(output.snapshots).toHaveLength(scenario.steps.length)
      expect(await readFile(join(home, '.claude/settings.json'), 'utf8')).toBe(scenario.files?.['.claude/settings.json'] ?? protectedUserSettings)
      return [
        { kind: 'state', value: { decoded: output, source: canonicalJsonSource(stdout) } },
        { kind: 'persistence', value: await persistedFiles(home) },
        { kind: 'stdio', value: { exitCode, stderr } },
      ]
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill()
    if (child) await child.exited
    await rm(home, { recursive: true, force: true })
  }
}

describe('H5AccessService actual TS self replay', () => {
  for (const [name, scenario] of h5ServiceScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', scenario), await record('typescript', scenario)) }, 40_000)
  }
})

describe('H5AccessService Go business parity', () => {
  let buildHome: string
  beforeAll(async () => {
    buildHome = await mkdtemp(join(tmpdir(), 'h5-service-build-'))
    executable = join(buildHome, process.platform === 'win32' ? 'h5-service-fixture.exe' : 'h5-service-fixture')
    const child = Bun.spawn(['go', 'build', '-o', executable, './internal/h5access/testdata/fixture'], {
      cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome),
      stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill('SIGKILL'), 180_000)
    try {
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text(), new Response(child.stdout).text()])
      expect(code, stderr).toBe(0)
    } finally { clearTimeout(timer) }
  }, 190_000)
  afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })
  for (const [name, scenario] of h5ServiceScenarios) {
    test(name, async () => { assertTraceEqual(await record('typescript', scenario), await record('go', scenario)) }, 40_000)
  }
})
