#!/usr/bin/env bun
/** Explicitly re-record request contracts from the frozen MAIN implementation. */
import { createHash } from 'node:crypto'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createRequestRetryHost } from './request-retry-host'

const reference = '6d21316ec4635f29ecbaa43e1a5574bfd31d7155'
const root = resolve(import.meta.dir, '../../..')
const evidence = join(root, 'artifacts/go-main-sync/request-contracts')
const digest = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex')
const files = [
  'backend/internal/provider/testdata/client_options_contract.json',
  'backend/internal/requestcontext/testdata/request_context_contract.json',
  'backend/internal/requestretry/testdata/source_contract.json',
  'backend/internal/requestcontext/testdata/effort_environment_contract.json',
]

async function verifyReference() {
  if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Capture requires pinned Bun 1.3.14')
  const manifest = JSON.parse(await readFile(join(root, `artifacts/go-main-sync/source-snapshots/${reference}.manifest.json`), 'utf8')) as { sourceSha: string; files: { path: string; sha256: string }[] }
  if (manifest.sourceSha !== reference) throw new Error('Wrong frozen reference manifest')
  const production = manifest.files.filter(file => file.path.startsWith('src/') && !/(?:\.test\.[cm]?[jt]sx?$|\/(?:__tests__|__fixtures__|testdata)\/)/.test(file.path))
  for (const file of production) if (digest(await readFile(join(root, file.path))) !== file.sha256) throw new Error(`Frozen MAIN production bytes changed: ${file.path}`)
  const expected = Bun.spawnSync(['git', 'show', `${reference}:preload.ts`], { cwd: root, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  if (expected.exitCode !== 0 || digest(expected.stdout) !== digest(await readFile(join(root, 'preload.ts')))) throw new Error('Frozen MAIN preload changed')
  const sdk = JSON.parse(await readFile(join(root, 'node_modules/@anthropic-ai/sdk/package.json'), 'utf8')).version
  if (sdk !== '0.128.0') throw new Error(`Wrong installed SDK ${sdk}`)
  return { sourceSha: reference, productionFiles: production.length, sdk, bun: Bun.version, revision: Bun.revision }
}

async function runFixture(name: string, input: any, flags: Record<string, string>) {
  const home = await mkdtemp(join(tmpdir(), 'main-request-capture-'))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    const payload = { ...input, homeDir: home, ...(name === 'request-retry' ? { home } : {}) }
    child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(import.meta.dir, `${name}.fixture.ts`)], {
      cwd: home,
      env: createSandboxedTestEnvironment(home, { NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), ...flags }),
      stdin: new Blob([JSON.stringify(name === 'request-retry' ? Object.fromEntries(Object.entries(payload).filter(([key]) => key !== 'homeDir')) : payload)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      if (code !== 0 || stderr !== '') throw new Error(`${name} failed: ${code} ${stderr.slice(-3000)}`)
      const result = JSON.parse(stdout)
      const blocked = (result.capabilities ?? result.sourceOnly?.capabilities)?.blocked
      if (!Array.isArray(blocked) || blocked.length !== 0) throw new Error(`${name} crossed its capability boundary`)
      return result
    } finally { clearTimeout(timer) }
  } finally {
    child?.kill()
    if (child) await child.exited
    await rm(home, { recursive: true, force: true })
  }
}

async function main() {
  if (!process.argv.includes('--write')) throw new Error('Pass --write to explicitly replace the MAIN source contracts')
  const beforeReference = await verifyReference()
  await mkdir(evidence, { recursive: true })
  const original = await Promise.all(files.map(async path => ({ path, bytes: await readFile(join(root, path)) })))
  for (const file of original) {
    try { await writeFile(join(evidence, `base-${file.path.split('/').at(-1)}`), file.bytes, { flag: 'wx' }) }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error }
  }
  const records: { path: string; oldSha256: string; newSha256: string; cases: number }[] = []

  const client = JSON.parse(original[0]!.bytes.toString())
  const recordedClient = await runFixture('client-options', client, { CC_HAHA_CLIENT_OPTIONS_FIXTURE: '1' })
  client.results = recordedClient.results
  client.resolverResults = recordedClient.resolverResults
  client.source = beforeReference
  const requests = JSON.parse(original[1]!.bytes.toString())
  for (const row of requests) {
    const recorded = await runFixture('request-context', row.scenario, { CC_HAHA_REQUEST_CONTEXT_FIXTURE: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' })
    row.observation = recorded.observation
    row.sourceOnly = recorded.sourceOnly
  }
  const retries = JSON.parse(original[2]!.bytes.toString())
  for (const row of retries.cases) {
    const host = createRequestRetryHost(row.input.mode)
    try {
      const input = { ...row.input, env: { ...row.input.env, ANTHROPIC_BASE_URL: host.origin } }
      const recorded = await runFixture('request-retry', input, { CC_HAHA_REQUEST_RETRY_FIXTURE: '1' })
      row.expected = { ...recorded.observation, server: await host.settled() }
      row.sourceOnly = { ...recorded.sourceOnly, wireRequests: host.wireRequests }
    } finally { await host.close() }
  }
  retries.source = 'Frozen MAIN src/services/api/claude.ts → withRetry → actual auth/client + SDK 0.128.0; original input histories/env only; physical loopback Source outputs are assertions'
  retries.reference = beforeReference
  const efforts = JSON.parse(original[3]!.bytes.toString())
  for (const row of efforts) {
    const recorded = await runFixture('request-context', row.scenario, { CC_HAHA_REQUEST_CONTEXT_FIXTURE: '1', ANTHROPIC_API_KEY: 'offline-fixture-key' })
    row.observation = recorded.observation
    row.sourceOnly = recorded.sourceOnly
  }
  const afterReference = await verifyReference()
  if (JSON.stringify(beforeReference) !== JSON.stringify(afterReference)) throw new Error('Reference changed while recording')
  for (const [index, result] of [client, requests, retries, efforts].entries()) {
    const bytes = Buffer.from(JSON.stringify(result, null, 2) + '\n')
    const file = original[index]!
    records.push({ path: file.path, oldSha256: digest(file.bytes), newSha256: digest(bytes), cases: index === 0 ? client.steps.length : index === 1 ? requests.length : index === 2 ? retries.cases.length : efforts.length })
    await writeFile(join(root, file.path), bytes)
  }
  await writeFile(join(evidence, 'receipt.json'), JSON.stringify({ beforeReference, afterReference, records }, null, 2) + '\n')
  console.log(`Recorded frozen MAIN request contracts: ${records.map(record => `${record.path}: ${record.cases}`).join('; ')}`)
}

main().catch(error => { console.error(error instanceof Error ? error.stack : error); process.exitCode = 1 })
