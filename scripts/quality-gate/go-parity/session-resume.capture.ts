import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'

if (Bun.version !== '1.3.14' || process.argv.slice(2).join(' ') !== '--capture-frozen-native-golden') throw new Error('Explicit offline Bun1.3.14 capture command required')
const root = fileURLToPath(new URL('../../../', import.meta.url)), home = await mkdtemp(join(tmpdir(), 'session-resume-capture-'))
try {
  const child = Bun.spawn([process.execPath, '--no-env-file', 'test', 'scripts/quality-gate/go-parity/session-resume.test.ts', 'scripts/quality-gate/go-parity/session-resume-request.test.ts', '--timeout', '190000'], { cwd: root, env: createSandboxedTestEnvironment(home), stdout: 'inherit', stderr: 'inherit' })
  const code = await child.exited
  if (code) throw new Error('Source self replay and independent Go parity must pass before native capture')
  const raw = await readFile(join(root, 'artifacts/go-parity/session-resume/source-contract.json'))
  if (JSON.parse(raw.toString()).contracts.length < 70) throw new Error('Incomplete native Source capture')
  await writeFile(join(root, 'backend/internal/sessionresume/testdata/source_contract.json'), raw)
  const requests = join(root, 'artifacts/go-parity/session-resume/first-request')
  const names = (await readdir(requests)).filter(name => name.endsWith('.json')).sort()
  if (names.length !== 8) throw new Error('Incomplete actual release Source first-request capture')
  const contracts = await Promise.all(names.map(async name => JSON.parse(await readFile(join(requests, name), 'utf8'))))
  await writeFile(join(root, 'backend/internal/sessionresume/testdata/first_request_contract.json'), JSON.stringify(contracts.map(({ input, source, scope }) => ({ input, source, scope })), null, 2) + '\n')
} finally { await rm(home, { recursive: true, force: true }) }
