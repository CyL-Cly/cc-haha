import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createGoBuildEnvironment } from '../../pr/go-dependencies'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { h5FixtureGroups, type H5FixtureGroup } from './h5-fixtures'
import { assertTraceEqual, canonicalJsonSource, type TraceEvent } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string

beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'h5-parity-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'access.test.exe' : 'access.test')
  const child = Bun.spawn(['go', 'test', '-c', '-o', executable, './internal/access'], {
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

async function record(kind: 'typescript' | 'go', fixture: H5FixtureGroup): Promise<TraceEvent[]> {
  const home = await mkdtemp(join(tmpdir(), 'h5-parity-record-'))
  const path = join(home, 'fixture.json')
  await writeFile(path, JSON.stringify(fixture))
  const child = Bun.spawn(kind === 'go' ? [executable, '-test.run=^TestH5ParityFixture$'] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/h5-legacy.ts')], {
    cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_H5_FIXTURE: path }),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill('SIGKILL'), 15_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, stderr).toBe(0)
    const prefix = kind === 'go' ? 'GO_H5_RESULT:' : 'TS_H5_RESULT:'
    const line = stdout.split('\n').find(value => value.startsWith(prefix))
    expect(line, stdout + stderr).toBeDefined()
    const source = line!.slice(prefix.length)
    return [{ kind: 'permission', value: { decoded: JSON.parse(source), source: canonicalJsonSource(source) } }]
  } finally {
    clearTimeout(timer)
    child.kill()
    await child.exited
    await rm(home, { recursive: true, force: true })
  }
}

for (const fixture of h5FixtureGroups) {
  test(`H5 actual TS self replay and Go parity: ${fixture.name}`, async () => {
    const expected = await record('typescript', fixture)
    assertTraceEqual(expected, await record('typescript', fixture))
    assertTraceEqual(expected, await record('go', fixture))
  }, 40_000)
}
