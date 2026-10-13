#!/usr/bin/env bun
/** Re-record historical static oracles through the actual frozen MAIN code. */
import { createHash } from 'node:crypto'
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile, stat, utimes } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { createSandboxedTestEnvironment } from '../../pr/test-environment'
import { createRequestRetryHost } from './request-retry-host'
import { streamHistoryScenarios } from './stream-history-fixtures'
import { canonicalJsonSource } from './trace'
import { controllerCases } from './session-controller-fixtures'
import { requestContextWire } from './request-context-fixtures'
import { streamHttpHeaderNames, streamHttpScenarios } from './stream-http-fixtures'
import { createHTTPSRequestHost } from './https-request-fixtures'

const root = resolve(import.meta.dir, '../../..')
const reference = '6d21316ec4635f29ecbaa43e1a5574bfd31d7155'
const evidence = join(root, 'artifacts/go-main-sync/feature-contracts')
const digest = (bytes: Uint8Array) => createHash('sha256').update(bytes).digest('hex')
const records: any[] = []
const revision = '0d9b296af33f2b851fcbf4df3e9ec89751734ba4'
async function verifyReference() {
  if (Bun.version !== '1.3.14' || Bun.revision !== revision) throw new Error('Pinned Bun required')
  const manifest = JSON.parse(await readFile(join(root, `artifacts/go-main-sync/source-snapshots/${reference}.manifest.json`), 'utf8'))
  if (manifest.sourceSha !== reference) throw new Error('Wrong source reference')
  for (const file of manifest.files.filter((file: any) => file.path.startsWith('src/') && !/(?:\.test\.[cm]?[jt]sx?$|\/(?:__tests__|__fixtures__|testdata)\/)/.test(file.path))) {
    if (digest(await readFile(join(root, file.path))) !== file.sha256) throw new Error(`Frozen MAIN changed: ${file.path}`)
  }
  const preload = Bun.spawnSync(['git', 'show', `${reference}:preload.ts`], { cwd: root, stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' })
  if (preload.exitCode !== 0 || digest(preload.stdout) !== digest(await readFile(join(root, 'preload.ts')))) throw new Error('Frozen MAIN preload changed')
  const sdk = JSON.parse(await readFile(join(root, 'node_modules/@anthropic-ai/sdk/package.json'), 'utf8')).version
  if (sdk !== '0.128.0') throw new Error('Wrong MAIN SDK')
  return { reference, sdk, bun: Bun.version, revision: Bun.revision }
}
const provenance = await verifyReference()
if (!process.argv.includes('--write')) throw new Error('Explicit --write required')
const selected = process.argv.find(value => value.startsWith('--only='))?.slice(7).split(',')
const allowed = (name: string) => !selected || selected.includes(name)
await mkdir(evidence, { recursive: true })
async function record(path: string, produce: (value: any) => Promise<any>, count: (value: any) => number, serialize = (value: any) => Buffer.from(JSON.stringify(value, null, 2) + '\n')) {
  const full = join(root, 'backend/internal', path)
  const before = await readFile(full)
  const backup = join(evidence, path.replaceAll('/', '-') + '.before.json')
  try { await writeFile(backup, before, { flag: 'wx' }) } catch (e) { if ((e as NodeJS.ErrnoException).code !== 'EEXIST') throw e }
  const value = await produce(JSON.parse(before.toString()))
  await verifyReference()
  const after = serialize(value)
  await writeFile(full, after)
  records.push({ path, cases: count(value), oldSha256: digest(before), newSha256: digest(after) })
  await writeFile(join(evidence, `receipt-${selected?.join('-') ?? 'all'}.json`), JSON.stringify({ provenance, records }, null, 2) + '\n')
  console.log(`Recorded ${path}: ${count(value)} actual MAIN cases`)
}
async function childFixture(name: string, payload: any, flags: Record<string, string>, setup?: (home: string) => Promise<any>) {
  const home = await realpath(await mkdtemp(join(tmpdir(), 'main-feature-capture-')))
  let child: ReturnType<typeof Bun.spawn> | undefined
  try {
    await mkdir(join(home, '.claude'), { recursive: true })
    const extra = setup ? await setup(home) : {}
    const input = { ...payload, ...extra, homeDir: home, home }
    if (['request-retry', 'query-loop', 'query-request', 'session-controller', 'session-resume-request', 'native-sdk-host-static'].includes(name)) delete input.homeDir
    child = Bun.spawn([process.execPath, '--no-env-file', '--feature=TRANSCRIPT_CLASSIFIER', join(import.meta.dir, `${name}.fixture.ts`)], {
      cwd: home, env: createSandboxedTestEnvironment(home, { NODE_ENV: 'production', TZ: 'UTC', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: '1', CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION: '0', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_CODE_MAX_RETRIES: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0', CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1', ANTHROPIC_API_KEY: 'offline-fixture-key', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), ...['session-controller', 'native-sdk-host-static', 'native-auto-classifier-static'].includes(name) ? { PATH: join(home, 'bin'), USE_BUILTIN_RIPGREP: 'false' } : {}, ...(name === 'native-sdk-host-static' ? { NODE_EXTRA_CA_CERTS: join(home, 'original-ca.pem') } : {}), ...flags }),
      stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
    })
    const timer = setTimeout(() => child?.kill('SIGKILL'), 30_000)
    try {
      const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
      if (code !== 0 || stderr !== '') throw new Error(`${name} failed ${code}: ${stderr.slice(-3500)} ${stdout.slice(-1000)}`)
      const output = ['session-controller', 'native-sdk-host-static'].includes(name) ? JSON.parse(await readFile(join(home, name === 'session-controller' ? 'controller-result.json' : 'sdk-full-ask-result.json'), 'utf8')) : JSON.parse(stdout)
      const caps = output.capabilities ?? output.sourceOnly?.capabilities ?? output.caps
      if (!caps || !Array.isArray(caps.blocked) || caps.blocked.length) throw new Error(`${name} missing clean capability proof: ${JSON.stringify(caps)}`)
      return { ...output, captureHome: home, captureStdout: stdout }
    } finally { clearTimeout(timer) }
  } finally { child?.kill(); if (child) await child.exited; await rm(home, { recursive: true, force: true }) }
}
if (allowed('sdkstream')) await record('provider/testdata/sdk_stream_contract.json', async value => {
  for (const row of value.cases) {
    const source = await childFixture('sdk-stream', row.scenario, { CC_HAHA_SDK_STREAM_FIXTURE: '1' })
    row.sdkVersion = source.sdkVersion; row.engineVersion = source.engineVersion; row.observation = source.observation
  }
  value.sdkVersion = provenance.sdk; value.source = provenance
  return value
}, value => value.cases.length)

if (allowed('streamhttp')) await record('streamhistory/testdata/http_source_contract.json', async value => {
  for (const row of value.cases) {
    const scenario = streamHttpScenarios.find(item => item.name === row.name)
    if (!scenario) throw new Error(`Missing HTTP stream scenario ${row.name}`)
    const socket: any[] = []
    const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      const url = new URL(request.url)
      socket.push({ method: request.method, path: url.pathname + url.search, bodyBytes: Buffer.from(await request.arrayBuffer()).toString('hex'), headers: Object.fromEntries(streamHttpHeaderNames.map(name => [name, request.headers.get(name)])) })
      return new Response(scenario.wire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'fixed-request' } })
    } })
    try {
      const source = await childFixture('stream-http', { wire: scenario.wire, model: scenario.model, nonInteractive: scenario.nonInteractive, httpOrigin: server.url.origin }, { CC_HAHA_STREAM_HISTORY_FIXTURE: '1', CC_HAHA_STREAM_HTTP_FIXTURE: '1', ANTHROPIC_BASE_URL: server.url.origin }, async home => { await writeFile(join(home, '.claude', '.claude.json'), '{"userID":"fixture-http-device"}'); return {} })
      if (socket.length !== 1) throw new Error('HTTP stream capture requires one physical request')
      row.observation = JSON.parse(source.observationSource); row.request = socket[0]
    } finally { server.stop(true) }
  }
  value.provenance = { ...value.provenance, ...provenance, runtime: `Bun ${Bun.version}; @anthropic-ai/sdk ${provenance.sdk}` }
  return value
}, value => value.cases.length)

if (allowed('controller')) await record('sessioncontroller/testdata/source_contract.json', async rows => {
  for (const row of rows) {
    const socket: string[] = []
    const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) { socket.push(await request.text()); return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'controller-response' } }) } })
    try {
      const source = await childFixture('session-controller', {}, { CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', ANTHROPIC_BASE_URL: server.url.origin, ANTHROPIC_CUSTOM_HEADERS: 'x-client-request-id: original-fixture-trace', USE_BUILTIN_RIPGREP: 'false' }, async home => {
        const original = controllerCases(home).find(item => item.name === row.name)
        if (!original || original.pending) throw new Error(`Missing frozen controller case ${row.name}`)
        const info: any = {}
        for (const [path, bytes] of Object.entries(original.files)) {
          await mkdir(dirname(path), { recursive: true }); await writeFile(path, bytes); await utimes(path, 1700000000, 1700000000)
          const file = await stat(path); info[path] = { modified: file.mtimeMs, birth: file.birthtimeMs, size: file.size }
        }
        return { ...original.input, origin: server.url.origin, stat: info, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
      })
      if (JSON.stringify(socket) !== JSON.stringify(source.observation.requests)) throw new Error('Controller capture request differs from actual socket')
      const privateHome = source.captureHome
      row.expected = JSON.parse(JSON.stringify(source.observation).replaceAll(privateHome.replace(/[^a-zA-Z0-9]/g, '-'), '/__fixture_home__'.replace(/[^a-zA-Z0-9]/g, '-')).replaceAll(privateHome, '/__fixture_home__'))
    } finally { server.stop(true) }
  }
  return rows
}, rows => rows.length)

if (allowed('resume')) await record('sessionresume/testdata/first_request_contract.json', async rows => {
  for (const row of rows) {
    const socket: any[] = []
    const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      const url = new URL(request.url); socket.push({ method: request.method, path: url.pathname + url.search, body: await request.text(), headers: [...request.headers] })
      return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'resume-response' } })
    } })
    try {
      const source = await childFixture('session-resume-request', { classifier: true, origin: server.url.origin, promptSource: JSON.stringify('next original prompt\ud800'), runtime: { platform: process.platform, arch: process.arch, version: process.version } }, { CC_HAHA_SESSION_RESUME_REQUEST: '1', ANTHROPIC_BASE_URL: server.url.origin }, async home => {
        await writeFile(join(home, '.claude', '.claude.json'), '{"userID":"fixture-resume-device"}')
        await writeFile(join(home, 'session.jsonl'), row.input.entries.map((entry: any) => JSON.stringify(entry)).join('\n') + '\n')
        return {}
      })
      if (socket.length !== 1 || source.observation.requests[0].body !== socket[0].body) throw new Error('Resume capture requires one actual matching request')
      const { captureHome, captureStdout, ...original } = source
      row.source = original
    } finally { server.stop(true) }
  }
  return rows
}, rows => rows.length)
if (allowed('schema')) await record('toolschema/testdata/tool_schema_contract.json', async rows => {
  for (const row of rows) {
    const output = await childFixture('tool-schema', { ...row.scenario, runtime: { platform: process.platform, arch: process.arch, version: process.version } }, { CC_HAHA_TOOL_SCHEMA_FIXTURE: '1' })
    row.observation = output.observation; row.sourceOnly = output.sourceOnly
  }
  return rows
}, rows => rows.length)
if (allowed('metadata')) await record('provider/testdata/http_metadata_contract.json', async value => {
  const output = await childFixture('http-metadata', { cases: value.cases }, { CC_HAHA_HTTP_METADATA_FIXTURE: '1' })
  value.observation = output.observation; value.source = { ...provenance, sourceOnly: output.sourceOnly }
  return value
}, value => value.cases.length)
if (allowed('streamhistory')) await record('streamhistory/testdata/source_contract.json', async value => {
  for (const row of value.cases) {
    const scenario = streamHistoryScenarios.find(item => item.name === row.name)
    if (!scenario) throw new Error(`Missing stream scenario ${row.name}`)
    const source = await childFixture('stream-history', scenario, { CC_HAHA_STREAM_HISTORY_FIXTURE: '1', ANTHROPIC_BASE_URL: 'http://127.0.0.1:43210' })
    row.observation = JSON.parse(source.observationSource)
  }
  value.provenance = { ...value.provenance, ...provenance, runtime: `Bun ${Bun.version}; @anthropic-ai/sdk ${provenance.sdk}` }
  return value
}, value => value.cases.length)
if (allowed('sharedopener')) await record('requestretry/testdata/shared_opener_contract.json', async value => {
  for (const row of value.cases) {
    const host = createRequestRetryHost(row.input.mode)
    try {
      const source = await childFixture('request-retry', { ...row.input, env: { ...row.input.env, ANTHROPIC_BASE_URL: host.origin } }, { CC_HAHA_REQUEST_RETRY_FIXTURE: '1' })
      row.expected = { ...source.observation, server: await host.settled() }
    } finally { await host.close() }
  }
  value.reference = provenance
  return value
}, value => value.cases.length)
async function querySource(name: string, scenario: any, flags: Record<string, string>, requestID: string) {
  const requests: any[] = []
  const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
    const url = new URL(request.url)
    requests.push({ path: url.pathname + url.search, method: request.method, body: await request.text(), headers: [...request.headers] })
    const wire = scenario.wires ? scenario.wires[requests.length - 1] : scenario.wire
    if (!wire) return new Response('Unexpected fixture request', { status: 400 })
    return new Response(wire, { headers: { 'content-type': 'text/event-stream', 'request-id': requestID + (scenario.wires ? requests.length : '') } })
  } })
  try {
    const identity = { runtime: { platform: process.platform, arch: process.arch, version: process.version }, deviceID: 'fixture-query-device', sessionID: '00000000-0000-4000-8000-000000000000' }
    const source = await childFixture(name, { ...scenario, ...identity, origin: server.url.origin }, { ...flags, ANTHROPIC_BASE_URL: server.url.origin }, async home => { await writeFile(join(home, '.claude', '.claude.json'), JSON.stringify({ userID: identity.deviceID })); return {} })
    const observation = JSON.parse(source.observationSource)
    for (const [index, request] of observation.requests.entries()) {
      const physical = requests[index]
      if (!physical || request.path !== physical.path || request.method !== physical.method || request.body !== physical.body) throw new Error(`${name} declared request differs from physical socket`)
      for (const [key, value] of request.headers) if (new Headers(physical.headers).get(key) !== value) throw new Error(`${name} physical header differs: ${key}`)
    }
    if (requests.length !== observation.requests.length) throw new Error(`${name} request count differs`)
    return { observation, requests: observation.requests, source: canonicalJsonSource(source.observationSource) }
  } finally { server.stop(true) }
}
if (allowed('queryloop')) await record('queryloop/testdata/source_contract.json', async value => {
  for (const row of value.cases) {
    const source = await querySource('query-loop', row.scenario, { CC_HAHA_QUERY_LOOP_FIXTURE: '1' }, 'fixture-request-')
    row.observation = source.observation; row.requests = source.requests
  }
  value.reference = provenance
  return value
}, value => value.cases.length)
if (allowed('queryrequest')) await record('queryloop/testdata/request-fixture/source_contract.json', async value => {
  for (const row of value.cases) row.observation = (await querySource('query-request', row.input, { CC_HAHA_QUERY_REQUEST_FIXTURE: '1' }, 'query-request-response')).observation
  value.provenance = { ...value.provenance, ...provenance }
  return value
}, value => value.cases.length)

if (allowed('tls')) await record('tlsconfig/testdata/tls_pem_contract.json', async value => {
  const certificate = await readFile(join(import.meta.dir, 'testdata/private-loopback-certificate.pem'), 'utf8')
  const key = await readFile(join(import.meta.dir, 'testdata/private-loopback-key.pem'), 'utf8')
  const oldCertificate = value.rows.find((row: any) => row.name === 'valid').originalExtra
  for (const row of value.rows) {
    row.originalExtra = row.originalExtra.replaceAll(oldCertificate, certificate).replaceAll(oldCertificate.replaceAll('\n', '\r\n'), certificate.replaceAll('\n', '\r\n')).replaceAll(oldCertificate.trimEnd(), certificate.trimEnd())
    let posts = 0
    const server = Bun.serve({ hostname: '127.0.0.1', port: 0, tls: { cert: certificate, key }, async fetch(request) {
      if (request.method !== 'POST' || new URL(request.url).pathname !== '/v1/messages') throw new Error('Unexpected PEM fixture request')
      posts++; await request.text()
      return Response.json({ id: 'private-response', type: 'message', role: 'assistant', model: 'deepseek-flash', content: [{ type: 'text', text: 'private' }], stop_reason: 'end_turn', stop_sequence: null, usage: { input_tokens: 1, output_tokens: 1 } })
    } })
    try {
      const source = await childFixture('native-tls-pem', { origin: server.url.origin }, { CC_HAHA_DIRECT_HTTPS_PROBE: '1' }, async home => {
        const ca = join(home, 'original.pem'); await writeFile(ca, row.originalExtra); return { ca }
      })
      row.outcome = source.observation; row.posts = posts
    } finally { server.stop(true) }
  }
  value.source = { ...value.source, ...provenance }
  return value
}, value => value.rows.length)

for (const profile of ['allow', 'block']) if (allowed('automode')) {
  const path = `automode/testdata/actual-permission-${profile}-body.json`
  await record(path, async () => {
    const requests: string[] = []
    const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      requests.push(await request.text())
      return Response.json({ id: 'msg_actual_classifier', type: 'message', role: 'assistant', model: 'fixture-model', content: [{ type: 'tool_use', id: 'classify_1', name: 'classify_result', input: { thinking: 'private authorized action', shouldBlock: profile === 'block', reason: 'private safe action' } }], stop_reason: 'tool_use', stop_sequence: null, usage: { input_tokens: 7, output_tokens: 3 } })
    } })
    try {
      const source = await childFixture('native-auto-classifier-static', { origin: server.url.origin, classify: true, permission: true, exit: 'transition' }, { CC_HAHA_AUTO_PLAN_FIXTURE: '1', ANTHROPIC_BASE_URL: server.url.origin, ANTHROPIC_MODEL: 'fixture-model', CLAUDE_CODE_ENTRYPOINT: 'cli', USER_TYPE: 'external' }, async home => {
        await mkdir(join(home, 'work/.claude'), { recursive: true })
        await writeFile(join(home, '.claude', '.claude.json'), '{"lastMigrationVersion":11,"userID":"original-fixture-device"}')
        await writeFile(join(home, '.claude', 'settings.json'), '{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true}}')
        return {}
      })
      if (!source.classified || requests.length !== 1) throw new Error('Actual classifier must execute one physical request')
      return requests[0]!
    } finally { server.stop(true) }
  }, () => 1, value => Buffer.from(value))
}

if (allowed('model-terminal')) await record('modelyield/testdata/source_terminal.json', async golden => {
  const certificate = await readFile(join(import.meta.dir, 'testdata/private-loopback-certificate.pem'), 'utf8')
  const key = await readFile(join(import.meta.dir, 'testdata/private-loopback-key.pem'), 'utf8')
  for (const row of golden.cases) {
    const home = await realpath(await mkdtemp(join(tmpdir(), 'main-model-terminal-')))
    const server = await createHTTPSRequestHost('sequence', certificate, key, home)
    try {
      await mkdir(join(home, '.claude'))
      const input = structuredClone(row.input)
      input.home = home
      input.env.ANTHROPIC_BASE_URL = server.origin
      if (row.input.name === 'creation-malformed') {
        input.env.NODE_EXTRA_CA_CERTS = join(home, 'original-ca.pem')
        await writeFile(input.env.NODE_EXTRA_CA_CERTS, 'private-invalid-pem-input\n')
      }
      input.runtime = { platform: process.platform, arch: process.arch, version: process.version }
      const child = Bun.spawn([process.execPath, '--no-env-file', join(import.meta.dir, 'https-request.fixture.ts')], {
        cwd: home, env: createSandboxedTestEnvironment(home, { CC_HAHA_HTTPS_REQUEST_FIXTURE: '1', CC_HAHA_FIXTURE_TEMP_ROOT: tmpdir(), NODE_ENV: 'production', CLAUDE_CODE_SIMPLE: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: '0' }),
        stdin: new Blob([JSON.stringify(input)]), stdout: 'pipe', stderr: 'pipe',
      })
      const timer = setTimeout(() => child.kill('SIGKILL'), 30_000)
      let stdout: string, stderr: string, code: number
      try { [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]) } finally { clearTimeout(timer) }
      const state = await server.settled()
      await writeFile(join(evidence, `model-terminal-${input.name}.raw.json`), JSON.stringify({ input, code, stdout, stderr, state, wireRequests: server.wireRequests }, null, 2))
      if (code !== 0 || stderr) throw new Error(`Actual model terminal failed: ${code} ${stderr}`)
      const source = JSON.parse(stdout)
      if (source.sourceOnly.sdk !== '0.128.0' || source.sourceOnly.capabilities.blocked.length || state.requests !== 0 || state.closedTCP !== state.acceptedTCP) throw new Error('Model terminal Source left declared SDK/transport boundary')
      row.input = input
      row.expectedTerminal = source.sourceOnly.queryTerminal.yielded.at(-1)
      row.expectedNotices = source.observation.notices
      row.sourceRawSha256 = digest(Buffer.from(stdout))
    } finally { server.close(); await rm(home, { recursive: true, force: true }) }
  }
  return { ...golden, sourceSha: reference, sdk: provenance.sdk, bun: provenance.bun, revision: provenance.revision }
}, golden => golden.cases.length)

if (allowed('sdkhost')) await record('sdkhost/testdata/source_contract.json', async rows => {
  const certificate = await readFile(join(import.meta.dir, 'testdata/private-loopback-certificate.pem'), 'utf8')
  const key = await readFile(join(import.meta.dir, 'testdata/private-loopback-key.pem'), 'utf8')
  for (const row of rows) {
    const sockets = new Set<Bun.Socket<any>>()
    const requests: string[] = []
    let first: Bun.Socket<any> | undefined, firstUser = false, released = false, prefixSent = false, finalSent = false
    const chunk = (socket: Bun.Socket<any>, text: string) => socket.write(`${Buffer.byteLength(text).toString(16)}\r\n${text}\r\n`)
    const send = () => {
      if (first && firstUser && !prefixSent) {
        prefixSent = true
        first.write('HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: first-response\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n')
        chunk(first, row.wires.prefix)
        if (row.name === 'empty-first') { finalSent = true; chunk(first, row.wires.firstTail); first.end('0\r\n\r\n') }
      }
      if (first && prefixSent && released && !finalSent) { finalSent = true; chunk(first, row.wires.firstTail); first.end('0\r\n\r\n') }
    }
    const server = Bun.listen<{ buffer: Buffer; done: boolean }>({ hostname: '127.0.0.1', port: 0, tls: { cert: certificate, key }, socket: {
      open(socket) { socket.data = { buffer: Buffer.alloc(0), done: false }; sockets.add(socket) },
      close(socket) { sockets.delete(socket) },
      data(socket, bytes) {
        if (socket.data.done) return
        socket.data.buffer = Buffer.concat([socket.data.buffer, bytes])
        const end = socket.data.buffer.indexOf('\r\n\r\n'); if (end < 0) return
        const header = socket.data.buffer.subarray(0, end).toString()
        const length = Number(header.match(/\r\ncontent-length:\s*(\d+)/i)?.[1] ?? 0)
        if (socket.data.buffer.length < end + 4 + length) return
        socket.data.done = true
        const [method, path] = header.split('\r\n')[0]!.split(' ')
        if (method === 'GET' && ['/__fixture/first-user-persisted', '/__fixture/release-final'].includes(path!)) {
          if (path === '/__fixture/first-user-persisted') firstUser = true; else released = true
          send(); socket.end('HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK'); return
        }
        if (method !== 'POST' || path !== '/v1/messages?beta=true') throw new Error('Unexpected SDK static fixture request')
        requests.push(socket.data.buffer.subarray(end + 4, end + 4 + length).toString())
        if (requests.length === 1) { first = socket; send() }
        else if (requests.length === 2) socket.end(`HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: second-response\r\nContent-Length: ${Buffer.byteLength(row.wires.secondWire)}\r\nConnection: close\r\n\r\n${row.wires.secondWire}`)
        else throw new Error('SDK static fixture exceeded two original requests')
      },
    } })
    const origin = `https://127.0.0.1:${server.port}`
    try {
      const source = await childFixture('native-sdk-host-static', {}, { CC_HAHA_SESSION_CONTROLLER_FIXTURE: '1', ANTHROPIC_BASE_URL: origin }, async home => {
        const project = home.replace(/[^a-zA-Z0-9]/g, '-')
        const relocate = (value: string) => value.replaceAll('<home>', home).replaceAll('<project>', project)
        for (const [path, bytes] of Object.entries(row.files) as [string, string][]) {
          await mkdir(dirname(relocate(path)), { recursive: true }); await writeFile(relocate(path), relocate(bytes)); await utimes(relocate(path), 1700000000, 1700000000)
        }
        await writeFile(join(home, 'original-ca.pem'), certificate)
        const info = Object.fromEntries(Object.entries(row.input.stat).map(([path, value]) => [relocate(path), value]))
        return { ...row.input, home, origin, stat: info, runtime: { platform: process.platform, arch: process.arch, version: process.version } }
      })
      if (requests.length !== 2 || JSON.stringify(requests) !== JSON.stringify(source.observation.requests)) throw new Error('SDK host declared requests differ from physical requests')
      const home = source.captureHome
      const project = home.replace(/[^a-zA-Z0-9]/g, '-')
      const captured: Record<string, any> = { stdout: source.captureStdout }
      for (const key of ['historySource', 'transcripts', 'requests', 'cache', 'appState']) captured[key] = source.observation[key]
      row.expected = JSON.parse(JSON.stringify(captured).replaceAll(project, '<project>').replaceAll(home, '<home>'))
    } finally { for (const socket of sockets) socket.end(); server.stop(true) }
  }
  return rows
}, rows => rows.length)
