import { afterAll, beforeAll, expect, test } from 'bun:test'
import { mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { assertTraceEqual } from './trace'

const rootDir = fileURLToPath(new URL('../../../', import.meta.url))
let buildHome: string
let executable: string
beforeAll(async () => {
  buildHome = await mkdtemp(join(tmpdir(), 'go-access-build-'))
  executable = join(buildHome, process.platform === 'win32' ? 'access-fixture.exe' : 'access-fixture')
  // The shared offline Go environment will copy only the explicitly prepared,
  // verified dependency seed into this disposable build home.
  const { createGoBuildEnvironment } = await import('../../pr/go-dependencies')
  const child = Bun.spawn(['go', 'build', '-o', executable, './internal/access/testdata/fixture'], {
    cwd: resolve(rootDir, 'backend'), env: await createGoBuildEnvironment(rootDir, buildHome),
    stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
  })
  const timer = setTimeout(() => child.kill(), 180_000)
  try {
    const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(code, `${stdout}${stderr}`).toBe(0)
  } finally { clearTimeout(timer) }
}, 190_000)
afterAll(async () => { if (buildHome) await rm(buildHome, { recursive: true, force: true }) })

type Fixture = {
  apiKey: string; localToken: string; petToken: string; headers: Record<string, string>;
  override?: string | null; validH5Tokens: string[]; origin: string; h5Enabled: boolean; allowOrigin: boolean;
}
const base: Fixture = { apiKey: 'fixture-api', localToken: 'fixture-desktop', petToken: 'fixture-pet', headers: {}, validH5Tokens: ['fixture-phone'], origin: '', h5Enabled: true, allowOrigin: false }
const authFixtures: Array<[string, Partial<Fixture>]> = [
  ['absent header', {}],
  ['empty tokens', { apiKey: '', localToken: '', petToken: '' }],
  ['API key', { headers: { Authorization: 'Bearer fixture-api' }, override: 'wrong' }],
  ['desktop token', { headers: { authorization: 'Bearer fixture-desktop' } }],
  ['pet token is independent', { headers: { Authorization: 'Bearer fixture-pet' } }],
  ['H5 token', { headers: { Authorization: 'Bearer fixture-phone' } }],
  ['wrong token', { headers: { Authorization: 'Bearer wrong' } }],
  ['extra bearer words', { headers: { Authorization: 'Bearer fixture-api ignored' } }],
  ['double space', { headers: { Authorization: 'Bearer  fixture-api' } }],
  ['tab bearer', { headers: { Authorization: 'Bearer\tfixture-api' } }],
  ['lowercase bearer', { headers: { Authorization: 'bearer fixture-api' } }],
  ['wrong scheme', { headers: { Authorization: 'Basic fixture-api' } }],
  ['token override local', { override: 'fixture-desktop' }],
  ['token override H5', { headers: { Authorization: 'invalid' }, override: 'fixture-phone' }],
  ['empty override suppresses header', { headers: { Authorization: 'Bearer fixture-phone' }, override: '' }],
  ['null override falls back', { headers: { Authorization: 'Bearer fixture-phone' }, override: null }],
  ['configured JavaScript whitespace', { localToken: '\ufeff fixture-desktop \u2028', headers: { Authorization: 'Bearer fixture-desktop' } }],
  ['NEL does not trim', { localToken: '\u0085fixture-desktop\u0085', override: '\u0085fixture-desktop\u0085' }],
  ['empty API key valid H5', { apiKey: '', headers: { Authorization: 'Bearer fixture-phone' } }],
  ['configured empty whitespace', { localToken: ' \ufeff\u2029', petToken: ' \t' }],
]
const origins = ['', 'file://', 'http://localhost:3000', 'http://127.0.0.1:2024', 'http://127.0.1.1', 'http://[::1]:5173', 'http://127.1', 'http://0x7f000001', 'http://0177.0.0.1', 'http://[0:0:0:0:0:0:0:1]', 'http://[::ffff:127.0.0.1]', 'http://localhost.', 'http://127.example.com', 'http://127.bad.0.1', 'http://127.0.0.999', 'http://192.168.0.20:2024', 'https://10.0.0.5', 'not-a-url', 'http://tauri.localhost', 'https://tauri.localhost', 'tauri://localhost', 'https://phone.example']
const fixtures = [
  ...authFixtures.map(([name, overrides]) => [name, { ...base, ...overrides }] as const),
  ...origins.map(origin => [`H5 CORS ${origin || '(absent)'}`, { ...base, origin }] as const),
  ['inactive H5 allows arbitrary origin', { ...base, origin: 'https://phone.example', h5Enabled: false }] as const,
  ['explicit H5 origin allowed', { ...base, origin: 'https://phone.example', allowOrigin: true }] as const,
]

async function capture(kind: 'typescript' | 'go') {
  const home = await mkdtemp(join(tmpdir(), 'go-access-parity-'))
  try {
    const child = Bun.spawn(kind === 'go' ? [executable] : [process.execPath, '--no-env-file', join(rootDir, 'scripts/quality-gate/go-parity/access.fixture.ts')], {
      cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_ACCESS_FIXTURE_HOME: home }),
      stdin: new Blob([JSON.stringify(fixtures.map(([, value]) => value))]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child.kill(), 20_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      expect(code, `${kind}: ${stderr}`).toBe(0)
      expect(stderr).toBe('')
      return JSON.parse(stdout) as unknown[]
    } finally { clearTimeout(timer); child.kill() }
  } finally { await rm(home, { recursive: true, force: true }) }
}

test('actual TS auth/CORS self replay and Go preserve credentials, headers, errors and callback order', async () => {
  const expected = await capture('typescript')
  const repeat = await capture('typescript')
  const actual = await capture('go')
  expect(expected.length).toBe(fixtures.length)
  expect(actual.length).toBe(fixtures.length)
  for (let index = 0; index < fixtures.length; index++) {
    const trace = (value: unknown) => [{ kind: fixtures[index][0], value }]
    assertTraceEqual(trace(expected[index]), trace(repeat[index]))
    assertTraceEqual(trace(expected[index]), trace(actual[index]))
  }
}, 50_000)
