import { afterEach, describe, expect, test } from 'bun:test'
import { appendFileSync, chmodSync, existsSync, linkSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { commitCompletedPair, createStreamEvidence, evidenceFiles, freezeResolvedDependencies, invalidateBatch, loadCompletedPairs, parseActualMessage, requireValidBatch, revalidateEngineEvidence, sealEngineEvidence, validateBatchStartup, validateEngineEvidence, verifyCandidateEvidence, verifySourceParentEvidence, type ProvenPair } from './go-main-paired-benchmark-evidence'
import { PAIRED_PROTOCOL, sha256, taskFor } from './go-main-paired-benchmark-core'
import { lanesForMode } from '../quality-gate/modes'

const roots: string[] = []
const temporary = () => { const root = mkdtempSync(join(tmpdir(), 'go-benchmark-evidence-')); roots.push(root); return realpathSync(root) }
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }) })

describe('complete dependency identity', () => {
  test('physical code, native bytes, directory topology and modes are frozen', () => {
    const source = temporary(), modules = join(source, 'node_modules')
    mkdirSync(join(modules, 'sdk'), { recursive: true })
    writeFileSync(join(modules, 'sdk/package.json'), '{"version":"0.128.0"}')
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const retries=2')
    writeFileSync(join(modules, 'sdk/engine.node'), Buffer.from([0, 255, 1]))
    const first = freezeResolvedDependencies(source)
    expect(first.rows.find(row => row.path === 'sdk/engine.node')?.hash).toHaveLength(64)
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const retries=99')
    const code = freezeResolvedDependencies(source)
    expect(code.hash).not.toBe(first.hash)
    writeFileSync(join(modules, 'sdk/engine.node'), Buffer.from([0, 255, 2]))
    const native = freezeResolvedDependencies(source)
    expect(native.hash).not.toBe(code.hash)
    mkdirSync(join(modules, 'new-empty-directory'))
    const directory = freezeResolvedDependencies(source)
    expect(directory.hash).not.toBe(native.hash)
    chmodSync(join(modules, 'sdk/client.mjs'), 0o700)
    expect(freezeResolvedDependencies(source).hash).not.toBe(directory.hash)
  })
  test('readonly canonical installation, internal aliases, hardlinks and cycles are deterministic', () => {
    const source = temporary(), shared = temporary(), modules = join(shared, 'installed')
    mkdirSync(join(modules, 'sdk'), { recursive: true })
    writeFileSync(join(modules, 'sdk/client.mjs'), 'export const ok=true')
    linkSync(join(modules, 'sdk/client.mjs'), join(modules, 'sdk/client-copy.mjs'))
    symlinkSync(modules, join(source, 'node_modules'))
    symlinkSync('sdk', join(modules, 'alias'))
    symlinkSync('..', join(modules, 'sdk/root-cycle'))
    symlinkSync('sdk/root-cycle/sdk/root-cycle/sdk/client.mjs', join(modules, 'finite-repeated-alias'))
    chmodSync(join(modules, 'sdk/client.mjs'), 0o400)
    const first = freezeResolvedDependencies(source, { canonicalRoot: modules })
    expect(first).toEqual(freezeResolvedDependencies(source, { canonicalRoot: modules }))
    expect(first.rows.find(row => row.path === 'alias')).toMatchObject({ type: 'symlink', target: 'sdk' })
    expect(first.rows.find(row => row.path === 'sdk/root-cycle')).toMatchObject({ type: 'symlink', target: '.' })
    expect(first.rows.find(row => row.path === 'finite-repeated-alias')).toMatchObject({ type: 'symlink', target: 'sdk/client.mjs' })
    expect(first.rows.filter(row => row.type === 'file')).toHaveLength(2)
    expect(first.rows.find(row => row.path === 'sdk/client.mjs')?.hash).toBe(first.rows.find(row => row.path === 'sdk/client-copy.mjs')?.hash)
    expect(() => freezeResolvedDependencies(source, { canonicalRoot: source })).toThrow('canonical root')
  })
  test('external and unresolved aliases are rejected before reading protected bytes', () => {
    const source = temporary(), modules = join(source, 'node_modules'), external = temporary()
    mkdirSync(modules)
    writeFileSync(join(external, 'protected'), 'not a dependency')
    symlinkSync(join(external, 'protected'), join(modules, 'escape'))
    expect(() => freezeResolvedDependencies(source)).toThrow('escapes canonical root')
    rmSync(join(modules, 'escape'))
    symlinkSync('b', join(modules, 'a'))
    symlinkSync('a', join(modules, 'b'))
    expect(() => freezeResolvedDependencies(source)).toThrow('symlink cycle')
    rmSync(join(modules, 'a'))
    rmSync(join(modules, 'b'))
    symlinkSync('growing/descendant', join(modules, 'growing'))
    expect(() => freezeResolvedDependencies(source)).toThrow('symlink cycle or depth')
  })
})

describe('bounded physical stream evidence', () => {
  test('complete bytes are forwarded exactly and EOF is a success', async () => {
    const bytes = new Uint8Array([0, 255, 240, 159, 152, 128])
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes.subarray(0, 3)); controller.enqueue(bytes.subarray(3)); controller.close() } }), { timeoutMs: 1000 })
    expect(new Uint8Array(await new Response(evidence.consumer).arrayBuffer())).toEqual(bytes)
    expect(await evidence.completion).toEqual({ complete: true, partialBytes: bytes })
    evidence.abort()
    expect((await evidence.completion).complete).toBe(true)
  })
  test('timeout settles even if the underlying cancel never settles', async () => {
    const bytes = new TextEncoder().encode('partial')
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes) }, cancel() { return new Promise(() => {}) } }), { timeoutMs: 30 })
    const consumer = new Response(evidence.consumer).arrayBuffer().catch(() => null)
    const result = await evidence.completion
    expect(result).toEqual({ complete: false, partialBytes: bytes, error: 'Evidence capture timeout' })
    expect(evidence.signal.aborted).toBe(true)
    expect(await consumer).toBeNull()
  })
  test('external abort and read/write errors preserve partial evidence without rejection', async () => {
    const caller = new AbortController()
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('first')) } }), { timeoutMs: 1000, signal: caller.signal })
    const reader = evidence.consumer.getReader()
    await reader.read()
    caller.abort()
    expect(await evidence.completion).toMatchObject({ complete: false, error: 'Evidence caller canceled' })
    const failed = createStreamEvidence(new ReadableStream({ start(controller) { controller.error(new Error('raw stream fault')) } }), { timeoutMs: 1000 })
    expect(await failed.completion).toMatchObject({ complete: false, error: 'raw stream fault' })
    const write = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(new Uint8Array([1])) } }), { timeoutMs: 1000, onChunk() { throw new Error('evidence disk fault') } })
    expect(await write.completion).toEqual({ complete: false, partialBytes: new Uint8Array([1]), error: 'evidence disk fault' })
  })
  test('excess capture size cannot be reported as complete', async () => {
    const bytes = new Uint8Array(32 * 1024 * 1024)
    const evidence = createStreamEvidence(new ReadableStream({ start(controller) { controller.enqueue(bytes); controller.enqueue(new Uint8Array([1])); controller.close() } }), { timeoutMs: 1000 })
    expect(await evidence.completion).toMatchObject({ complete: false, error: 'Evidence capture exceeded byte limit' })
    expect((await evidence.completion).partialBytes.byteLength).toBe(bytes.byteLength)
    expect(() => createStreamEvidence(new ReadableStream(), { timeoutMs: 1000, maxBytes: 1 })).toThrow('at least 32 MiB')
  })
  test('loopback HTTP consumer cancellation aborts upstream and settles the receipt', async () => {
    let upstreamClosed = false
    const upstream = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch(request) {
      request.signal.addEventListener('abort', () => { upstreamClosed = true }, { once: true })
      return new Response(new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('data: live-only-fixture\n\n')) }, cancel() { upstreamClosed = true } }), { headers: { 'content-type': 'text/event-stream' } })
    } })
    let capture: ReturnType<typeof createStreamEvidence> | undefined
    const proxy = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
      const controller = new AbortController()
      request.signal.addEventListener('abort', () => controller.abort(), { once: true })
      const response = await fetch(upstream.url, { signal: controller.signal })
      capture = createStreamEvidence(response.body!, { abortController: controller, timeoutMs: 1000 })
      return new Response(capture.consumer)
    } })
    try {
      const caller = new AbortController()
      const response = await fetch(proxy.url, { signal: caller.signal })
      const first = await response.body!.getReader().read()
      expect(new TextDecoder().decode(first.value)).toBe('data: live-only-fixture\n\n')
      caller.abort()
      const result = await capture!.completion
      expect(result.complete).toBe(false)
      expect(new TextDecoder().decode(result.partialBytes)).toBe('data: live-only-fixture\n\n')
      expect(capture!.signal.aborted).toBe(true)
      const deadline = performance.now() + 1000
      while (!upstreamClosed && performance.now() < deadline) await Bun.sleep(5)
      expect(upstreamClosed).toBe(true)
    } finally { await proxy.stop(true); await upstream.stop(true) }
  })
})

// Synthetic wire fixtures exercise the recorder's fail-closed decisions only.
// They are never live-model, product-equivalence or benchmark-score evidence.
const actualUsage = { input_tokens: 11, output_tokens: 7, cache_read_input_tokens: 3, cache_creation_input_tokens: 2 }
const wireMessage = (id: string, model: string = PAIRED_PROTOCOL.model) => [
  { type: 'message_start', message: { id, model, usage: { ...actualUsage, output_tokens: 0 } } },
  { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'ok' } },
  { type: 'message_delta', delta: { stop_reason: 'end_turn' }, usage: { output_tokens: 7 } },
  { type: 'message_stop' },
].map(event => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join('')

function engineFiles(home: string, task = taskFor('arithmetic', 0), callers: ('main' | 'agent' | 'unknown')[] = ['main']) {
  mkdirSync(home, { recursive: true })
  for (const [path, value] of Object.entries(task.files)) {
    const parts = path.split('/'); parts.pop()
    if (parts.length) mkdirSync(join(home, ...parts), { recursive: true })
    writeFileSync(join(home, path), value)
  }
  const requests: unknown[] = [], publicRows: unknown[] = [], transport: unknown[] = []
  for (const [index, caller] of callers.entries()) {
    const number = index + 1, id = 'message-' + number
    const request = { number, method: 'POST', pathname: '/anthropic/v1/messages', query: '?beta=true', body: { model: PAIRED_PROTOCOL.model, max_tokens: caller === 'main' ? 32000 : 512, thinking: { type: 'disabled' }, stream: true, messages: [{ role: 'user', content: 'fixture' }] } }
    const bodyText = JSON.stringify(request.body)
    const captured = { ...request, bodyHash: sha256(bodyText) }
    writeFileSync(join(home, `request-${number}.txt`), bodyText)
    const raw = wireMessage(id)
    requests.push(captured); transport.push({ number, method: 'POST', pathname: request.pathname, query: request.query })
    writeFileSync(join(home, `response-${number}.txt`), raw)
    writeFileSync(join(home, `response-${number}.json`), JSON.stringify({ method: 'POST', status: 200, complete: true, contentType: 'text/event-stream', sha256: sha256(raw), requestHash: sha256(JSON.stringify(captured)), bytes: Buffer.byteLength(raw) }))
    if (caller !== 'unknown') publicRows.push({ type: 'assistant', parent_tool_use_id: caller === 'main' ? null : 'actual-parent-tool-id', message: { id, model: PAIRED_PROTOCOL.model, usage: actualUsage } })
  }
  const mainCount = callers.filter(caller => caller === 'main').length, count = callers.length
  const usage = Object.fromEntries(Object.entries(actualUsage).map(([name, value]) => [name, value * mainCount]))
  const model = { inputTokens: 11 * count, outputTokens: 7 * count, cacheReadInputTokens: 3 * count, cacheCreationInputTokens: 2 * count, costUSD: .01, contextWindow: 1_000_000, maxOutputTokens: 32000 }
  publicRows.push({ type: 'result', subtype: 'success', is_error: false, num_turns: 1, total_cost_usd: .01, usage, modelUsage: { [PAIRED_PROTOCOL.model]: model } })
  writeFileSync(join(home, 'requests.jsonl'), requests.map(row => JSON.stringify(row) + '\n').join(''))
  writeFileSync(join(home, 'transport.jsonl'), transport.map(row => JSON.stringify(row) + '\n').join(''))
  writeFileSync(join(home, 'stdout.jsonl'), publicRows.map(row => JSON.stringify(row) + '\n').join(''))
  writeFileSync(join(home, 'stderr.log'), '')
  writeFileSync(join(home, 'judge.log'), 'actual fixture judge output\n')
  writeFileSync(join(home, 'judge.json'), JSON.stringify({ exitCode: 0, timedOut: false, codeHash: sha256(task.judge), logHash: sha256('actual fixture judge output\n') }))
  const args = ['fixture-cli', '--print', '--model', PAIRED_PROTOCOL.model, '--thinking', 'disabled', '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', '20', task.prompt]
  writeFileSync(join(home, 'command.json'), JSON.stringify({ argv: args, exitCode: 0, timedOut: false, startedMonotonicMs: 5, endedMonotonicMs: 105, durationMs: 100, evidenceErrors: [], recordingErrors: [], transportCalls: count, modelRequests: count }))
}

describe('actual wire and terminal accounting evidence', () => {
  test('main, real public child and unknown auxiliary are distinct and all tokens reconcile', () => {
    const home = temporary(); engineFiles(home, undefined, ['main', 'agent', 'unknown'])
    const evidence = validateEngineEvidence(home, taskFor('arithmetic', 0), '')
    expect(evidence.exchanges.map(row => row.caller)).toEqual(['main', 'agent', 'unknown-auxiliary'])
    expect(evidence.aggregate).toEqual({ input_tokens: 33, output_tokens: 21, cache_read_input_tokens: 9, cache_creation_input_tokens: 6 })
    expect(evidence.main).toEqual(actualUsage)
    expect(evidence.measurement).toEqual({ success: true, durationMs: 100, estimatedCost: .01, runtimeErrors: [] })
    expect(evidence.estimatedCostBasis).toContain('not a DeepSeek invoice')
  })
  test('an actual failed task with complete accounting stays failed without inventing a missing cost', () => {
    const home = temporary(), task = taskFor('arithmetic', 0); engineFiles(home, task)
    const rows = readFileSync(join(home, 'stdout.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line))
    rows.at(-1).subtype = 'error_max_turns'; rows.at(-1).is_error = true
    writeFileSync(join(home, 'stdout.jsonl'), rows.map(row => JSON.stringify(row) + '\n').join(''))
    const actual = validateEngineEvidence(home, task, '')
    expect(actual.measurement.success).toBe(false)
    expect(actual.measurement.estimatedCost).toBe(.01)
    expect(() => sealEngineEvidence(home, task, 'source', 'manifest', '')).not.toThrow()
  })
  test('partial SSE, provider error, changed response model and bad cache usage are rejected', () => {
    const valid = wireMessage('real-id')
    expect(parseActualMessage(valid, 'text/event-stream').usage).toEqual(actualUsage)
    expect(() => parseActualMessage(valid.slice(0, -1), 'text/event-stream')).toThrow('Truncated')
    expect(() => parseActualMessage(valid.replace(/event: message_stop[\s\S]*$/, ''), 'text/event-stream')).toThrow('message_stop')
    expect(() => parseActualMessage('data: {"type":"error"}\n\n', 'text/event-stream')).toThrow('provider stream error')
    expect(() => parseActualMessage(JSON.stringify({ type: 'message', stop_reason: 'end_turn', usage: { ...actualUsage, cache_creation: { ephemeral_1h_input_tokens: 99 } } }), 'application/json')).toThrow('Cache creation')
    const home = temporary(); engineFiles(home)
    const raw = wireMessage('message-1', 'other-model'), request = JSON.parse(readFileSync(join(home, 'requests.jsonl'), 'utf8'))
    writeFileSync(join(home, 'response-1.txt'), raw)
    writeFileSync(join(home, 'response-1.json'), JSON.stringify({ status: 200, method: 'POST', complete: true, contentType: 'text/event-stream', sha256: sha256(raw), bytes: Buffer.byteLength(raw), requestHash: sha256(JSON.stringify(request)) }))
    expect(() => validateEngineEvidence(home, taskFor('arithmetic', 0), '')).toThrow('response model')
  })
  test('unmatched ledger, missing usage, thinking and main/aux limits fail closed', () => {
    const task = taskFor('arithmetic', 0)
    for (const change of ['ledger', 'usage', 'main-limit', 'aux-limit', 'thinking', 'missing-thinking', 'null-thinking', 'aux-missing-thinking', 'time']) {
      const home = temporary(); engineFiles(home, task, ['main', 'unknown'])
      const stdout = readFileSync(join(home, 'stdout.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line))
      if (change === 'ledger') stdout.at(-1).modelUsage[PAIRED_PROTOCOL.model].outputTokens++
      if (change === 'usage') delete stdout.at(-1).usage.output_tokens
      if (['main-limit', 'aux-limit', 'thinking', 'missing-thinking', 'null-thinking', 'aux-missing-thinking'].includes(change)) {
        const rows = readFileSync(join(home, 'requests.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line))
        const index = change === 'aux-limit' || change === 'aux-missing-thinking' ? 1 : 0
        if (change === 'thinking') rows[index].body.thinking = { type: 'enabled', budget_tokens: 10 }
        else if (change === 'missing-thinking' || change === 'aux-missing-thinking') delete rows[index].body.thinking
        else if (change === 'null-thinking') rows[index].body.thinking = null
        else rows[index].body.max_tokens = change === 'main-limit' ? 100 : 32001
        const bodyText = JSON.stringify(rows[index].body)
        rows[index].bodyHash = sha256(bodyText)
        writeFileSync(join(home, `request-${index + 1}.txt`), bodyText)
        writeFileSync(join(home, 'requests.jsonl'), rows.map(row => JSON.stringify(row) + '\n').join(''))
        const response = JSON.parse(readFileSync(join(home, `response-${index + 1}.json`), 'utf8'))
        response.requestHash = sha256(JSON.stringify(rows[index]))
        writeFileSync(join(home, `response-${index + 1}.json`), JSON.stringify(response))
      }
      if (change === 'time') { const row = JSON.parse(readFileSync(join(home, 'command.json'), 'utf8')); row.durationMs = 1; writeFileSync(join(home, 'command.json'), JSON.stringify(row)) }
      writeFileSync(join(home, 'stdout.jsonl'), stdout.map(row => JSON.stringify(row) + '\n').join(''))
      expect(() => validateEngineEvidence(home, task, '')).toThrow()
    }
  })
})

describe('sealed task failures without optional stopping', () => {
  test('missing terminal, zero-model crash, all HTTP failures, partial timeout and deleted immutable input remain failed observations', () => {
    for (const failure of ['missing-terminal', 'zero-model', 'all-503', 'partial-timeout', 'immutable-delete', 'zero-cost']) {
      const home = temporary(), task = taskFor('arithmetic', 0)
      engineFiles(home, task, failure === 'zero-model' ? [] : ['main'])
      const command = JSON.parse(readFileSync(join(home, 'command.json'), 'utf8'))
      const records = readFileSync(join(home, 'stdout.jsonl'), 'utf8').trim().split('\n').map(line => JSON.parse(line))
      if (failure === 'missing-terminal') records.pop()
      if (failure === 'zero-model') {
        records.length = 0
        command.exitCode = 1
        writeFileSync(join(home, 'stderr.log'), 'capability is not migrated: actual fixture startup failure\n')
      }
      if (failure === 'all-503' || failure === 'partial-timeout') {
        const request = JSON.parse(readFileSync(join(home, 'requests.jsonl'), 'utf8'))
        const raw = failure === 'all-503' ? JSON.stringify({ type: 'error', error: { type: 'overloaded_error', message: 'synthetic private peer' } }) : wireMessage('message-1').slice(0, -20)
        writeFileSync(join(home, 'response-1.txt'), raw)
        writeFileSync(join(home, 'response-1.json'), JSON.stringify({ method: 'POST', status: failure === 'all-503' ? 503 : 200, complete: failure === 'all-503', ...(failure === 'partial-timeout' ? { error: 'Evidence caller canceled' } : {}), contentType: failure === 'all-503' ? 'application/json' : 'text/event-stream', sha256: sha256(raw), bytes: Buffer.byteLength(raw), requestHash: sha256(JSON.stringify(request)) }))
        records.length = 0
        command.exitCode = failure === 'all-503' ? 1 : 143
        command.timedOut = failure === 'partial-timeout'
      }
      if (failure === 'immutable-delete') rmSync(join(home, task.immutable[0]))
      if (failure === 'zero-cost') records.at(-1).total_cost_usd = 0
      writeFileSync(join(home, 'stdout.jsonl'), records.map(row => JSON.stringify(row) + '\n').join(''))
      writeFileSync(join(home, 'command.json'), JSON.stringify(command))
      const actual = validateEngineEvidence(home, task, '')
      expect(actual.measurement.success).toBe(false)
      if (failure === 'immutable-delete') {
        expect(actual.measurement.runtimeErrors).toContain('Immutable task or parent changed')
        expect(actual.measurement.estimatedCost).toBe(.01)
      } else {
        expect(actual.measurement.estimatedCost).toBe(0)
        expect(actual.accountingComplete).toBe(false)
        expect(actual.missingMeasurements).toEqual(['estimatedCost'])
        expect(actual.estimatedCostBasis).toContain('not a bill or an observed zero cost')
      }
      const sealed = sealEngineEvidence(home, task, 'source', 'manifest', '')
      expect(revalidateEngineEvidence(home, task, 'source', 'manifest', '', sealed.sha256)).toEqual(actual.measurement)
      appendFileSync(join(home, 'stdout.jsonl'), 'tampered after seal')
      expect(() => revalidateEngineEvidence(home, task, 'source', 'manifest', '', sealed.sha256)).toThrow('seal mismatch')
    }
  })
  test('raw hash loss and recorder faults remain evidence failures rather than failed task seals', () => {
    for (const failure of ['changed-wire', 'missing-wire', 'recorder-fault']) {
      const home = temporary(), task = taskFor('arithmetic', 0)
      engineFiles(home, task)
      if (failure === 'changed-wire') appendFileSync(join(home, 'response-1.txt'), 'changed')
      if (failure === 'missing-wire') rmSync(join(home, 'response-1.txt'))
      if (failure === 'recorder-fault') {
        const command = JSON.parse(readFileSync(join(home, 'command.json'), 'utf8'))
        command.recordingErrors.push('Evidence write failed: owned fixture disk fault')
        writeFileSync(join(home, 'command.json'), JSON.stringify(command))
      }
      expect(() => sealEngineEvidence(home, task, 'go', 'manifest', '')).toThrow()
      expect(existsSync(join(home, 'sealed-evidence.json'))).toBe(false)
    }
  })
  test('unjoined processes, forcibly closed output and judge capture faults cannot authorize a seal', () => {
    for (const failure of ['joined', 'stdoutComplete', 'stderrComplete', 'judge-recording']) {
      const home = temporary(), task = taskFor('arithmetic', 0)
      engineFiles(home, task)
      const path = join(home, failure === 'judge-recording' ? 'judge.json' : 'command.json')
      const receipt = JSON.parse(readFileSync(path, 'utf8'))
      if (failure === 'judge-recording') receipt.recordingErrors = ['actual fixture output capture fault']
      else receipt[failure] = false
      writeFileSync(path, JSON.stringify(receipt))
      expect(() => sealEngineEvidence(home, task, 'go', 'manifest', '')).toThrow('process or output cleanup')
      expect(existsSync(join(home, 'sealed-evidence.json'))).toBe(false)
    }
  })
})

describe('durable, nonselective batch evidence', () => {
  test('120 manually assembled JSON measurements cannot resume or score', () => {
    const root = temporary(), task = taskFor('arithmetic', 0)
    const measurement = { success: true, durationMs: 100, estimatedCost: 1, runtimeErrors: [] }
    writeFileSync(join(root, 'pairs.jsonl'), Array.from({ length: 120 }, () => JSON.stringify({ id: task.id, source: measurement, go: measurement }) + '\n').join(''))
    expect(() => loadCompletedPairs(root, [task], 'manifest', '')).toThrow('Legacy')
    expect(JSON.parse(readFileSync(join(root, 'invalid-batch.json'), 'utf8')).status).toBe('invalid-batch')
    writeFileSync(join(root, 'pairs.jsonl'), '')
    expect(() => requireValidBatch(root)).toThrow('permanently')
  })
  test('completed pair fully revalidates raw, judge, artifacts, usage and seal on resume', () => {
    const task = taskFor('arithmetic', 0), root = temporary(), manifest = sha256('frozen manifest')
    const pair = { id: task.id, evidence: { version: 2, manifestHash: manifest } } as ProvenPair
    for (const engine of ['source', 'go'] as const) {
      const home = join(root, task.id, engine); engineFiles(home, task)
      const sealed = sealEngineEvidence(home, task, engine, manifest, '')
      pair[engine] = sealed.measurement; pair.evidence[engine] = sealed.sha256
    }
    writeFileSync(join(root, 'pair-in-progress.json'), JSON.stringify({ id: task.id, manifestHash: manifest }))
    commitCompletedPair(root, task, pair, manifest, '', () => {})
    expect(loadCompletedPairs(root, [task], manifest, '')).toEqual([pair])
    const home = join(root, task.id, 'source')
    for (const file of ['response-1.txt', 'judge.log', 'stdout.jsonl', 'calc.py']) {
      const original = readFileSync(join(home, file))
      appendFileSync(join(home, file), 'changed')
      expect(() => revalidateEngineEvidence(home, task, 'source', manifest, '', pair.evidence.source)).toThrow('seal mismatch')
      writeFileSync(join(home, file), original)
    }
    const pairFile = join(root, 'pairs.jsonl'), original = readFileSync(pairFile, 'utf8')
    writeFileSync(pairFile, original.replace('"estimatedCost":0.01', '"estimatedCost":1'))
    expect(() => loadCompletedPairs(root, [task], manifest, '')).toThrow('actual raw evidence')
  })
  test('frozen-input failure happens before pair append and stays invalid after restoration', () => {
    const root = temporary(), task = taskFor('arithmetic', 0)
    writeFileSync(join(root, 'pair-in-progress.json'), '{}')
    expect(() => commitCompletedPair(root, task, {} as ProvenPair, 'manifest', '', () => { throw new Error('source changed') })).toThrow('source changed')
    expect(existsSync(join(root, 'pairs.jsonl'))).toBe(false)
    expect(() => requireValidBatch(root)).toThrow('permanently')
    const reason = readFileSync(join(root, 'invalid-batch.json'), 'utf8')
    invalidateBatch(root, 'cannot replace first failure')
    expect(readFileSync(join(root, 'invalid-batch.json'), 'utf8')).toBe(reason)
  })
  test('interrupted pair, foreign manifest and external artifact links cannot be resumed', () => {
    const root = temporary(); writeFileSync(join(root, 'pair-in-progress.json'), '{}')
    expect(() => requireValidBatch(root)).toThrow('Interrupted')
    const home = temporary(), task = taskFor('arithmetic', 0); engineFiles(home, task)
    const sealed = sealEngineEvidence(home, task, 'go', 'manifest', '')
    expect(() => revalidateEngineEvidence(home, task, 'go', 'other', '', sealed.sha256)).toThrow('seal mismatch')
    const external = temporary(); writeFileSync(join(external, 'private'), 'protected')
    symlinkSync(join(external, 'private'), join(home, 'unapproved'))
    expect(() => evidenceFiles(home)).toThrow('Unsupported')
  })
})

function candidateFiles(options: { link?: boolean; extraInputs?: Record<string, unknown>; defaultArtifacts?: boolean; productVersion?: string } = {}) {
  const root = temporary(), receiptRoot = temporary()
  const file = (path: string, text: string) => {
    const directory = path.slice(0, path.lastIndexOf('/')); mkdirSync(directory, { recursive: true })
    writeFileSync(path, text, { mode: 0o600 }); return { path, sha256: sha256(text) }
  }
  const files: Record<string, unknown> = Object.fromEntries(['backend/go.mod', 'package.json', 'scripts/cli/build.ts', 'desktop/package.json'].map(name => {
    const bytes = options.productVersion && name === 'desktop/package.json' ? JSON.stringify({ version: options.productVersion }) : options.productVersion && name === 'package.json' ? JSON.stringify({ version: '999.0.0-local' }) : 'candidate:' + name
    const proof = file(join(root, name), bytes)
    return [name, { kind: 'file', sha256: proof.sha256, mode: 0o600 }]
  }))
  // Preserve the actual capture schema: deleted tracked paths remain null.
  for (const platform of ['darwin', 'linux', 'portable', 'windows']) files[`backend/internal/mcpapi/name_sort_${platform}.go`] = null
  if (options.link) {
    const path = join(root, 'backend/source-link.go'), target = 'go.mod'
    symlinkSync(target, path)
    files['backend/source-link.go'] = { kind: 'symlink', mode: lstatSync(path).mode & 0o7777, target, sha256: sha256('symlink:' + target) }
  }
  Object.assign(files, options.extraInputs)
  const inputs = file(join(receiptRoot, 'inputs.json'), JSON.stringify({ preCopyPostIdentityEqual: true, allTrackedAndUntrackedFiles: Object.keys(files).length, files }))
  const binary = file(join(receiptRoot, 'ordinary-cli'), 'test-only candidate bytes')
  const target = process.platform + '/' + process.arch, goOS = process.platform === 'win32' ? 'windows' : process.platform, goArch = process.arch === 'x64' ? 'amd64' : process.arch
  const info = file(join(receiptRoot, 'build-info.txt'), `path\tgithub.com/NanmiCoder/cc-haha/backend/cmd/claude-haha\nbuild\t-trimpath=true\nbuild\tGOOS=${goOS}\nbuild\tGOARCH=${goArch}\n${options.productVersion ? 'build\t-ldflags=' + JSON.stringify('-X main.version=' + options.productVersion) + '\n' : ''}`)
  const required = lanesForMode('pr').filter(lane => !lane.live)
  const artifacts = options.defaultArtifacts ? join(root, 'artifacts/quality-runs') : join(receiptRoot, 'actual-quality-artifacts')
  const runId = 'candidate-gate-fixture'
  const logs = required.map(lane => file(join(artifacts, runId, 'logs', lane.id.replace(/[^a-zA-Z0-9._-]+/g, '-') + '.log'), 'synthetic passed lane fixture ' + lane.id))
  const reportObject = { runId, mode: 'pr', dryRun: false, allowLive: false, rootDir: root, summary: { failed: 0, skipped: 0, passed: required.length }, results: required.map((lane, index) => ({ id: lane.id, command: lane.command, status: 'passed', exitCode: 0, logPath: logs[index].path })) }
  const report = file(join(artifacts, runId, 'report.json'), JSON.stringify(reportObject))
  const bunPath = realpathSync(process.execPath)
  const bun = { path: bunPath, sha256: sha256(readFileSync(bunPath)), version: '1.3.14', revision: '0d9b296af33f2b851fcbf4df3e9ec89751734ba4' }
  const buildArgv = [bunPath, 'run', 'build:cli', ...(options.productVersion ? ['--version', options.productVersion] : [])]
  const qualityArgv = [bunPath, 'run', 'verify', ...(options.defaultArtifacts ? [] : ['--artifacts-dir', artifacts])]
  const assets = ['react', 'native-host', 'native-helper', 'native-assets'].map(role => {
    const proofs = [file(join(receiptRoot, role + '.bin'), 'synthetic asset ' + role)]
    const receipt = file(join(receiptRoot, role + '.json'), JSON.stringify({ role, inputsHash: inputs.sha256, files: proofs, exitCode: 0, ...(options.productVersion ? { productVersion: options.productVersion } : {}) }))
    return { role, inputsHash: inputs.sha256, files: proofs, receipt, ...(options.productVersion ? { productVersion: options.productVersion } : {}) }
  })
  const value = {
    version: 1, root, target, binary, inputs, bun,
    build: { argv: buildArgv, effectiveCommand: [...buildArgv], exitCode: 0, inputsBefore: inputs.sha256, inputsAfter: inputs.sha256, binaryHash: binary.sha256, infoBinaryHash: binary.sha256, info, log: file(join(receiptRoot, 'build.log'), 'synthetic ordinary build') },
    quality: { argv: qualityArgv, effectiveCommand: [...qualityArgv], exitCode: 0, inputsBefore: inputs.sha256, inputsAfter: inputs.sha256, report, logs, log: file(join(receiptRoot, 'verify.log'), 'synthetic complete verify') },
    assets,
  }
  const receipt = file(join(receiptRoot, 'candidate.json'), JSON.stringify(value))
  return { root, value, receipt, binary, target, reportObject, artifacts }
}

describe('candidate authorization evidence before provider quota', () => {
  test('real captured deletions, links and absolute pinned Bun artifact argv are accepted without rewriting the manifest', () => {
    for (const defaultArtifacts of [false, true]) {
      const { value, receipt, binary, target } = candidateFiles({ link: true, defaultArtifacts })
      const original = readFileSync(value.inputs.path)
      expect(verifyCandidateEvidence(receipt.path, binary.sha256, target).inputsHash).toBe(value.inputs.sha256)
      expect(readFileSync(value.inputs.path)).toEqual(original)
    }
  })
  test('restored deleted paths, unsupported directories and escaped link/input paths fail closed', () => {
    for (const restored of ['file', 'directory', 'dangling-link']) {
      const { root, receipt, binary, target } = candidateFiles()
      const path = join(root, 'backend/internal/mcpapi/name_sort_darwin.go')
      mkdirSync(join(root, 'backend/internal/mcpapi'), { recursive: true })
      if (restored === 'file') writeFileSync(path, 'restored')
      else if (restored === 'directory') mkdirSync(path)
      else symlinkSync('missing', path)
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('Deleted candidate source restored')
    }
    for (const extraInputs of [{ '../outside': null }, { 'backend/unsupported': { kind: 'directory', mode: 0o700, sha256: sha256('directory') } }]) {
      const { receipt, binary, target } = candidateFiles({ extraInputs })
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow()
    }
    const { root, receipt, binary, target } = candidateFiles({ link: true })
    rmSync(join(root, 'backend/source-link.go'))
    symlinkSync('/outside-private-state', join(root, 'backend/source-link.go'))
    expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('source link changed')
    const absent = candidateFiles({ extraInputs: { 'backend/escape/deleted.go': null } })
    symlinkSync(temporary(), join(absent.root, 'backend/escape'))
    expect(() => verifyCandidateEvidence(absent.receipt.path, absent.binary.sha256, absent.target)).toThrow('parent is not a physical directory')
  })
  test('actual command, executable and artifact paths reject normalization that hides filters or another Bun', () => {
    for (const change of ['filter', 'skip', 'dry-run', 'duplicate-artifacts', 'relative-artifacts', 'different-report', 'effective-command', 'basename', 'bun-hash', 'bun-revision']) {
      const { value, receipt, binary, target } = candidateFiles()
      if (change === 'filter') value.quality.argv.push('--only', 'go-checks')
      if (change === 'skip') value.quality.argv.push('--skip', 'coverage')
      if (change === 'dry-run') value.quality.argv.push('--dry-run')
      if (change === 'duplicate-artifacts') value.quality.argv.push('--artifacts-dir', value.quality.argv[4])
      if (change === 'relative-artifacts') value.quality.argv[4] = './artifacts'
      if (change === 'different-report') value.quality.argv[4] = value.root
      if (change !== 'effective-command') value.quality.effectiveCommand = [...value.quality.argv]
      if (change === 'effective-command') value.quality.effectiveCommand.push('--only', 'go-checks')
      if (change === 'basename') value.quality.argv[0] = 'bun'
      if (change === 'bun-hash') value.bun.sha256 = sha256('different executable')
      if (change === 'bun-revision') value.bun.revision = 'other revision'
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow()
    }
  })
  test('observed startup Source/candidate failure stays invalid after the exact input is restored', async () => {
    for (const changed of ['source', 'candidate']) {
      const { root, value, receipt, binary, target } = candidateFiles()
      const allocation = temporary()
      writeFileSync(join(allocation, 'manifest.json'), 'existing frozen batch\n')
      const path = changed === 'source' ? join(root, 'backend/go.mod') : value.binary.path
      const original = readFileSync(path)
      appendFileSync(path, 'observed changed startup input')
      await expect(validateBatchStartup(allocation, async () => verifyCandidateEvidence(receipt.path, binary.sha256, target))).rejects.toThrow()
      expect(existsSync(join(allocation, 'invalid-batch.json'))).toBe(true)
      writeFileSync(path, original)
      await expect(validateBatchStartup(allocation, async () => verifyCandidateEvidence(receipt.path, binary.sha256, target))).rejects.toThrow('permanently')
    }
  })
  test('all ordinary build, source, unfiltered gate logs and distinct assets are bound', () => {
    const { value, receipt, binary, target } = candidateFiles()
    expect(verifyCandidateEvidence(receipt.path, binary.sha256, target)).toMatchObject({ sha256: receipt.sha256, inputsHash: value.inputs.sha256, binaryHash: binary.sha256, target })
    expect(() => verifyCandidateEvidence(receipt.path, sha256('other binary'), target)).toThrow('binary/target')
    expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, 'other/target')).toThrow('binary/target')
    appendFileSync(value.quality.logs[0].path, 'changed')
    expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('Evidence file changed')
  })
  test('missing assets, selected/skipped quality, stale source and test-tag builds cannot authorize a batch', () => {
    for (const change of ['missing-asset', 'skipped', 'selected', 'source', 'extra-source', 'test-tag', 'wrong-build', 'wrong-inputs', 'asset-bytes']) {
      const { root, value, receipt, binary, target, reportObject } = candidateFiles()
      if (change === 'missing-asset') value.assets.pop()
      if (change === 'skipped' || change === 'selected') {
        if (change === 'skipped') reportObject.results[0].status = 'skipped'
        else reportObject.results.pop()
        writeFileSync(value.quality.report.path, JSON.stringify(reportObject))
        value.quality.report.sha256 = sha256(JSON.stringify(reportObject))
      }
      if (change === 'source') appendFileSync(join(root, 'backend/go.mod'), 'changed')
      if (change === 'extra-source') writeFileSync(join(root, 'backend/omitted.go'), 'package unexpected')
      if (change === 'test-tag') { const text = readFileSync(value.build.info.path, 'utf8') + 'build\t-tags=native_process_fixture\n'; writeFileSync(value.build.info.path, text); value.build.info.sha256 = sha256(text) }
      if (change === 'wrong-build') value.build.argv = ['go', 'test']
      if (change === 'wrong-inputs') value.quality.inputsAfter = sha256('different input epoch')
      if (change === 'asset-bytes') writeFileSync(value.assets[0].files[0].path, 'new asset after gate')
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow()
    }
  })
})

// Append to scripts/perf/go-main-paired-benchmark-evidence.test.ts.
// Add verifySourceParentEvidence to the existing evidence import.
// Uses existing candidateFiles(), temporary(), sha256, PAIRED_PROTOCOL and fs imports.
// Every byte below is synthetic fixture evidence; never live Source or benchmark evidence.
const parentHelperNames = ['record_source_parent.py', 'credential_capability.py', 'record-source-parent.ts', 'formal-command.ts', 'formal-transport.ts'] as const
const parentMarker = 'SOURCE_LIVE_FULL_READY'
const parentPrompt = 'Reply with exactly SOURCE_LIVE_FULL_READY and no other text. Do not use tools.'

function sourceParentFiles() {
  const built = candidateFiles()
  const write = (path: string, bytes: string | Uint8Array) => {
    mkdirSync(path.slice(0, path.lastIndexOf('/')), { recursive: true, mode: 0o700 })
    writeFileSync(path, bytes, { mode: 0o600 })
    chmodSync(path, 0o600)
    return { path, sha256: sha256(bytes) }
  }
  const json = (path: string, value: unknown) => write(path, JSON.stringify(value) + '\n')
  const candidateInputs = JSON.parse(readFileSync(built.value.inputs.path, 'utf8'))
  const helpers = parentHelperNames.map(name => {
    const local = 'scripts/perf/source-parent-recorder/' + name
    const proof = write(join(built.root, local), '# synthetic helper identity fixture: ' + name + '\n')
    candidateInputs.files[local] = { kind: 'file', mode: 0o600, sha256: proof.sha256 }
    return { name, ...proof }
  })
  const wrapper = write(join(built.root, 'scripts/perf/go-main-source-live.ts'), '// synthetic Source wrapper identity fixture\n')
  candidateInputs.files['scripts/perf/go-main-source-live.ts'] = { kind: 'file', mode: 0o600, sha256: wrapper.sha256 }
  candidateInputs.allTrackedAndUntrackedFiles = Object.keys(candidateInputs.files).length
  built.value.inputs = json(built.value.inputs.path, candidateInputs)
  for (const run of [built.value.build, built.value.quality]) run.inputsBefore = run.inputsAfter = built.value.inputs.sha256
  for (const asset of built.value.assets) {
    asset.inputsHash = built.value.inputs.sha256
    const receipt = JSON.parse(readFileSync(asset.receipt.path, 'utf8'))
    receipt.inputsHash = built.value.inputs.sha256
    asset.receipt = json(asset.receipt.path, receipt)
  }
  json(built.receipt.path, built.value)
  const candidate = verifyCandidateEvidence(built.receipt.path, built.binary.sha256, built.target)
  const sourceRoot = temporary()
  // The production parent protocol admits canonical POSIX /tmp allocations.
  const allocation = realpathSync(mkdtempSync(join(process.platform === 'darwin' ? '/private/tmp' : '/tmp', 'go-source-parent-proof-')))
  roots.push(allocation)
  chmodSync(allocation, 0o700)
  const sourceNames = ['src/entrypoints/cli.tsx', 'adapters/runtime.ts', 'stubs/platform.ts', 'preload.ts', 'package.json', 'bun.lock', 'tsconfig.json']
  const sourceProofs = sourceNames.map(name => {
    const bytes = 'synthetic frozen Source input: ' + name + '\n'
    const proof = write(join(sourceRoot, name), bytes)
    const gitBlob = createHash('sha1').update(`blob ${Buffer.byteLength(bytes)}\0`).update(bytes).digest('hex')
    return { path: name, gitBlob, sha256: proof.sha256 }
  })
  const sourceRows = sourceProofs.map(row => [row.path, row.sha256] as [string, string])
  // Full MAIN producer proofs also contain non-production paths; they must not
  // be compared directly to the runner's differently shaped production hash.
  const docs = 'synthetic non-production MAIN document\n'
  write(join(sourceRoot, 'docs/fixture.md'), docs)
  sourceProofs.push({ path: 'docs/fixture.md', gitBlob: createHash('sha1').update(`blob ${Buffer.byteLength(docs)}\0`).update(docs).digest('hex'), sha256: sha256(docs) })
  const context = { candidate, source: { root: sourceRoot, rows: sourceRows }, dependenciesHash: sha256('synthetic resolved dependency inventory'), wrapperHash: wrapper.sha256 }
  const home = join(allocation, 'source-home'), sid = '46464646-4646-4646-8646-464646464646'
  const message = { id: 'synthetic-parent-message-1', type: 'message', role: 'assistant', model: PAIRED_PROTOCOL.model, content: [{ type: 'text', text: parentMarker }], stop_reason: 'end_turn', stop_sequence: null, usage: { input_tokens: 11, output_tokens: 7, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 } }
  const records = [
    { type: 'user', uuid: '11111111-1111-4111-8111-111111111111', parentUuid: null, sessionId: sid, cwd: home, isSidechain: false, message: { role: 'user', content: parentPrompt } },
    { type: 'assistant', uuid: '22222222-2222-4222-8222-222222222222', parentUuid: '11111111-1111-4111-8111-111111111111', sessionId: sid, cwd: home, isSidechain: false, message },
  ]
  const transcriptBytes = records.map(row => JSON.stringify(row) + '\n').join('')
  const original = write(join(home, '.claude/projects/-synthetic-parent-project', sid + '.jsonl'), transcriptBytes)
  const frozen = write(join(allocation, 'frozen-parent.jsonl'), transcriptBytes)
  const publicRows = [
    { type: 'assistant', session_id: sid, parent_tool_use_id: null, message },
    { type: 'result', subtype: 'success', is_error: false, result: parentMarker, session_id: sid, num_turns: 1, total_cost_usd: .01, usage: message.usage, modelUsage: { [PAIRED_PROTOCOL.model]: { inputTokens: 11, outputTokens: 7, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: .01, contextWindow: PAIRED_PROTOCOL.contextWindow, maxOutputTokens: PAIRED_PROTOCOL.maxOutputTokens } } },
  ]
  const stdout = write(join(allocation, 'stdout.jsonl'), publicRows.map(row => JSON.stringify(row) + '\n').join(''))
  const stderr = write(join(allocation, 'stderr.log'), '')
  const driverLog = write(join(allocation, 'driver.log'), JSON.stringify({ status: 'real-source-pilot-complete-await-python-freeze', allocation }) + '\n')
  const supervision = json(join(allocation, 'worker-supervision.json'), { code: 0, credentialAdmitted: true, credentialStable: true, outputRedactions: 0, failure: null })
  const transportRoot = join(allocation, 'transport')
  const body = { model: PAIRED_PROTOCOL.model, max_tokens: PAIRED_PROTOCOL.maxOutputTokens, thinking: { type: PAIRED_PROTOCOL.thinking }, stream: true, messages: [{ role: 'user', content: parentPrompt }] }
  const bodyText = JSON.stringify(body)
  const request = { number: 1, method: 'POST', pathname: '/anthropic/v1/messages', query: '?beta=true', bodyHash: sha256(bodyText), body }
  const rawResponse = [
    { type: 'message_start', message: { ...message, content: [], stop_reason: null, usage: { ...message.usage, output_tokens: 0 } } },
    { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: parentMarker } },
    { type: 'content_block_stop', index: 0 },
    { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: { output_tokens: 7 } },
    { type: 'message_stop' },
  ].map(event => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join('')
  const transportFiles = [
    write(join(transportRoot, 'requests.jsonl'), JSON.stringify(request) + '\n'),
    write(join(transportRoot, 'transport.jsonl'), JSON.stringify({ number: 1, method: 'POST', pathname: request.pathname, query: request.query, upstreamURL: 'https://api.deepseek.com' + request.pathname + request.query }) + '\n'),
    write(join(transportRoot, 'request-1.txt'), bodyText),
    write(join(transportRoot, 'response-1.txt'), rawResponse),
    json(join(transportRoot, 'response-1.json'), { method: 'POST', status: 200, complete: true, bytes: Buffer.byteLength(rawResponse), sha256: sha256(rawResponse), contentType: 'text/event-stream', requestHash: sha256(JSON.stringify(request)) }),
  ].sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0)
  const pilotValue = {
    scope: 'real-Source-unscored-parent-pilot-not-120-benchmark',
    argv: [built.value.bun.path, '--no-env-file', wrapper.path, '--print', '--model', PAIRED_PROTOCOL.model, '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions', '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), parentPrompt],
    cwd: home, prompt: parentPrompt, candidate, source: sourceRoot, dependenciesHash: context.dependenciesHash, wrapperHash: wrapper.sha256, binaryHash: built.binary.sha256, bun: { path: built.value.bun.path, version: built.value.bun.version, revision: built.value.bun.revision, sha256: built.value.bun.sha256 },
    process: { code: 0, timedOut: false, startedMonotonicMs: 10, endedMonotonicMs: 110, durationMs: 100, processGroup: 12345, signal: null, joined: true, stdoutComplete: true, stderrComplete: true, recordingErrors: [] },
    transport: { calls: 1, modelCalls: 1, errors: [], transportErrors: [], recordingErrors: [], redactions: 0 }, outputRedactions: 0,
  }
  const pilot = json(join(allocation, 'pilot-process.json'), pilotValue)
  const inputsValue = { scope: 'unscored-real-source-parent-preparation', source: { main: PAIRED_PROTOCOL.main, root: sourceRoot, files: sourceProofs, hash: sha256(JSON.stringify(sourceProofs)) }, helpers: Object.fromEntries(helpers.map(row => [row.name, row.sha256])), binarySHA256: built.binary.sha256, bunSHA256: built.value.bun.sha256, candidateEvidenceSHA256: candidate.sha256 }
  const inputs = json(join(allocation, 'source-inputs.json'), inputsValue)
  const value = { version: 2, status: 'frozen-real-source-parent-not-formal120', parent: { sourcePath: original.path, frozenPath: frozen.path, sha256: frozen.sha256, bytes: Buffer.byteLength(transcriptBytes), originalAndCopyEqual: true, sessionId: sid, records: records.length, realSourceHistory: true }, inputs, pilot, supervision, stdout, stderr, driverLog, driverLogSHA256: driverLog.sha256, helpers, transportFiles, credentialAudit: 'single admitted K1 capability; exact fd/path identity and bytes stable; no credential fingerprint recorded', qualification: 'Synthetic test fixture only; never benchmark evidence.' }
  const receipt = json(join(allocation, 'frozen-parent-receipt.json'), value)
  const save = () => json(receipt.path, value)
  const rewrite = (field: 'inputs' | 'pilot' | 'supervision' | 'stdout' | 'stderr' | 'driverLog', mutate: (current: any) => void) => {
    const current = JSON.parse(readFileSync(value[field].path, 'utf8'))
    mutate(current)
    value[field] = json(value[field].path, current)
    save()
  }
  const rewriteTransport = (name: string, mutate: (current: any) => void) => {
    const proof = value.transportFiles.find(row => row.path === join(transportRoot, name))!
    const current = JSON.parse(readFileSync(proof.path, 'utf8'))
    mutate(current)
    Object.assign(proof, json(proof.path, current))
    save()
  }
  // Mirror the real startup boundary: first revalidate the full current
  // candidate (including assets), then admit its Source parent receipt.
  const verify = () => verifySourceParentEvidence(receipt.path, frozen.path, { ...context, candidate: verifyCandidateEvidence(context.candidate.path, built.binary.sha256, built.target) })
  return { allocation, built, context, value, receipt, original, frozen, sid, records, publicRows, transportRoot, write, json, save, rewrite, rewriteTransport, verify }
}

describe('real Source parent provenance admission with synthetic private fixtures', () => {
  test('complete actual-shape private pilot binds SID, production Source rows and the same four-asset candidate', () => {
    const f = sourceParentFiles()
    const before = [f.receipt.path, f.original.path, f.frozen.path].map(path => readFileSync(path))
    expect(f.verify()).toMatchObject({ path: f.receipt.path, sha256: sha256(readFileSync(f.receipt.path)), parentHash: f.value.parent.sha256, sessionId: f.sid, candidateHash: f.context.candidate.sha256 })
    expect([f.receipt.path, f.original.path, f.frozen.path].map(path => readFileSync(path))).toEqual(before)
  })
  test('complete actual Messages JSON also binds its real model/id/usage to public output', () => {
    const f = sourceParentFiles(), raw = JSON.stringify((f.publicRows[0] as any).message)
    const proof = f.value.transportFiles.find(row => row.path === join(f.transportRoot, 'response-1.txt'))!
    Object.assign(proof, f.write(proof.path, raw))
    f.rewriteTransport('response-1.json', row => { row.contentType = 'application/json'; row.bytes = Buffer.byteLength(raw); row.sha256 = sha256(raw) })
    expect(f.verify()).toBeTruthy()
  })
  test('missing final receipt, legacy status, flags, SID, record count and parent hash cannot admit a parent', () => {
    for (const failure of ['missing', 'legacy', 'status', 'real-history', 'copied', 'sid', 'records', 'sha', 'bytes']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (failure === 'missing') rmSync(f.receipt.path)
      else {
        if (failure === 'legacy') f.value.version = 1
        if (failure === 'status') f.value.status = 'prepared-not-run'
        if (failure === 'real-history') f.value.parent.realSourceHistory = false
        if (failure === 'copied') f.value.parent.originalAndCopyEqual = false
        if (failure === 'sid') f.value.parent.sessionId = '33333333-3333-4333-8333-333333333333'
        if (failure === 'records') f.value.parent.records++
        if (failure === 'sha') f.value.parent.sha256 = sha256('other parent')
        if (failure === 'bytes') f.value.parent.bytes++
        f.save()
      }
      expect(f.verify).toThrow()
    }
  })
  test('receipts, parent bytes and raw files require physical private files in the same private allocation', () => {
    for (const failure of ['receipt-mode', 'parent-mode', 'allocation-mode', 'parent-symlink', 'receipt-symlink', 'external-source', 'original-changed', 'unsealed-transport']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (failure === 'receipt-mode') chmodSync(f.receipt.path, 0o644)
      if (failure === 'parent-mode') chmodSync(f.frozen.path, 0o640)
      if (failure === 'allocation-mode') chmodSync(f.allocation, 0o755)
      if (failure === 'parent-symlink' || failure === 'receipt-symlink') {
        const path = failure === 'parent-symlink' ? f.frozen.path : f.receipt.path
        const bytes = readFileSync(path), target = join(temporary(), 'synthetic-private-file')
        f.write(target, bytes); rmSync(path); symlinkSync(target, path)
      }
      if (failure === 'external-source') { f.value.parent.sourcePath = f.write(join(temporary(), f.sid + '.jsonl'), readFileSync(f.original.path)).path; f.save() }
      if (failure === 'original-changed') appendFileSync(f.original.path, '{}\n')
      if (failure === 'unsealed-transport') f.write(join(f.transportRoot, 'unsealed-extra.txt'), 'synthetic omitted raw bytes')
      expect(f.verify).toThrow()
    }
  })
  test('actual Source user and assistant SID, final public result and transcript marker must agree', () => {
    for (const failure of ['user-sid', 'assistant-sid', 'assistant-marker', 'assistant-id', 'public-sid', 'public-assistant-sid', 'public-failure', 'duplicate-result', 'trailing-output']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (failure.startsWith('user-') || failure.startsWith('assistant-')) {
        if (failure === 'user-sid') f.records[0].sessionId = 'wrong-user-session'
        if (failure === 'assistant-sid') f.records[1].sessionId = 'wrong-assistant-session'
        if (failure === 'assistant-marker') (f.records[1].message as any).content = [{ type: 'text', text: 'wrong actual reply' }]
        if (failure === 'assistant-id') (f.records[1].message as any).id = 'unrelated-physical-assistant'
        const bytes = f.records.map(row => JSON.stringify(row) + '\n').join('')
        f.write(f.original.path, bytes); f.write(f.frozen.path, bytes)
        f.value.parent.sha256 = sha256(bytes); f.value.parent.bytes = Buffer.byteLength(bytes)
      } else {
        const terminal = f.publicRows[1] as any
        if (failure === 'public-sid') terminal.session_id = 'wrong-public-session'
        if (failure === 'public-assistant-sid') (f.publicRows[0] as any).session_id = 'wrong-public-assistant-session'
        if (failure === 'public-failure') terminal.is_error = true
        if (failure === 'duplicate-result') f.publicRows.push({ ...terminal })
        if (failure === 'trailing-output') f.publicRows.push({ type: 'system', subtype: 'synthetic-after-result' } as any)
        f.value.stdout = f.write(f.value.stdout.path, f.publicRows.map(row => JSON.stringify(row) + '\n').join(''))
      }
      f.save()
      expect(f.verify).toThrow()
    }
  })
  test('original fire-and-forget HEAD diagnostics do not become failed model POSTs', () => {
    const f = sourceParentFiles()
    f.rewrite('pilot', row => { row.transport.transportErrors = ['Synthetic preconnect cancellation before transport numbering'] })
    expect(f.verify()).toBeTruthy()
  })
  test('a complete model POST and an actual failed HEAD fetch remain qualified without rewriting that error', () => {
    for (const wrongRoute of [false, true]) {
      const f = sourceParentFiles(), error = 'Upstream fetch failed: synthetic HEAD connection refused'
      const route = wrongRoute ? '/anthropic/v1/unexpected-preconnect' : '/anthropic'
      const row = { number: 2, method: 'HEAD', pathname: route, query: '', upstreamURL: 'https://api.deepseek.com' + route }
      const transportProof = f.value.transportFiles.find(proof => proof.path === join(f.transportRoot, 'transport.jsonl'))!
      Object.assign(transportProof, f.write(transportProof.path, readFileSync(transportProof.path, 'utf8') + JSON.stringify(row) + '\n'))
      const failure = { method: 'HEAD', status: 0, complete: false, error, bytes: 0, sha256: sha256(new Uint8Array()) }
      const failedProof = f.json(join(f.transportRoot, 'response-2.json'), failure)
      f.value.transportFiles.push(failedProof)
      f.rewrite('pilot', pilot => { pilot.transport.calls = 2; pilot.transport.transportErrors = [error] })
      if (wrongRoute) expect(f.verify).toThrow('preconnect')
      else {
        const before = readFileSync(failedProof.path)
        expect(f.verify()).toBeTruthy()
        expect(readFileSync(failedProof.path)).toEqual(before)
        expect(JSON.parse(before.toString())).toEqual(failure)
      }
    }
  })
  test('FD supervision and complete real process observations are mandatory even when exit status is zero', () => {
    for (const failure of ['not-admitted', 'unstable', 'redacted', 'supervisor-error', 'supervisor-exit', 'process-exit', 'timeout', 'unjoined', 'stdout-open', 'stderr-open', 'recording-error', 'pilot-failure', 'pilot-redaction', 'zero-model', 'transport-error']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (['not-admitted', 'unstable', 'redacted', 'supervisor-error', 'supervisor-exit'].includes(failure)) f.rewrite('supervision', row => {
        if (failure === 'not-admitted') row.credentialAdmitted = false
        if (failure === 'unstable') row.credentialStable = false
        if (failure === 'redacted') row.outputRedactions = 1
        if (failure === 'supervisor-error') row.failure = 'synthetic FD supervision failure'
        if (failure === 'supervisor-exit') row.code = 9
      })
      else f.rewrite('pilot', row => {
        if (failure === 'process-exit') row.process.code = 1
        if (failure === 'timeout') row.process.timedOut = true
        if (failure === 'unjoined') row.process.joined = false
        if (failure === 'stdout-open') row.process.stdoutComplete = false
        if (failure === 'stderr-open') row.process.stderrComplete = false
        if (failure === 'recording-error') row.process.recordingErrors = ['synthetic pipe capture fault']
        if (failure === 'pilot-failure') row.failure = 'synthetic pilot failure'
        if (failure === 'pilot-redaction') row.outputRedactions = 1
        if (failure === 'zero-model') row.transport.modelCalls = 0
        if (failure === 'transport-error') row.transport.errors = ['synthetic model stream failure']
      })
      expect(f.verify).toThrow()
    }
  })
  test('pilot must bind the exact candidate, binary, Bun, Source, dependencies, wrapper and normal helper bytes', () => {
    for (const failure of ['candidate', 'binary', 'bun', 'source-root', 'dependency', 'wrapper', 'cli-entry', 'prompt', 'source-main', 'source-production', 'missing-production', 'duplicate-production', 'missing-helper', 'helper-replaced', 'unbound-helper', 'stale-asset']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (['candidate', 'binary', 'bun', 'source-root', 'dependency', 'wrapper', 'cli-entry', 'prompt'].includes(failure)) f.rewrite('pilot', row => {
        if (failure === 'candidate') row.candidate.sha256 = sha256('another four-asset candidate')
        if (failure === 'binary') row.binaryHash = sha256('another ordinary binary')
        if (failure === 'bun') row.bun.revision = 'unqualified-bun-revision'
        if (failure === 'source-root') row.source = temporary()
        if (failure === 'dependency') row.dependenciesHash = sha256('another installed dependency tree')
        if (failure === 'wrapper') row.wrapperHash = sha256('another wrapper')
        if (failure === 'cli-entry') row.argv.splice(row.argv.indexOf('--print'), 0, '--bare')
        if (failure === 'prompt') row.prompt = 'different actual parent request'
      })
      else if (['source-main', 'source-production', 'missing-production', 'duplicate-production'].includes(failure)) f.rewrite('inputs', row => {
        if (failure === 'source-main') row.source.main = '0'.repeat(40)
        if (failure === 'source-production') row.source.files[0].sha256 = sha256('another Source production input')
        if (failure === 'missing-production') row.source.files.shift()
        if (failure === 'duplicate-production') row.source.files.push({ ...row.source.files[0] })
        row.source.hash = sha256(JSON.stringify(row.source.files))
      })
      else if (failure === 'missing-helper') { f.value.helpers.pop(); f.save() }
      else if (failure === 'helper-replaced') appendFileSync(f.value.helpers[0].path, '# changed helper byte\n')
      else if (failure === 'unbound-helper') { const proof = f.write(join(temporary(), f.value.helpers[0].name), readFileSync(f.value.helpers[0].path)); Object.assign(f.value.helpers[0], proof); f.save() }
      else if (failure === 'stale-asset') appendFileSync(f.built.value.assets[0].files[0].path, ' changed after pilot')
      expect(f.verify).toThrow()
    }
  })
  test('sealed official raw model requests and responses cannot be omitted, changed or redirected', () => {
    for (const failure of ['missing-proof', 'duplicate-proof', 'changed-wire', 'wrong-origin', 'wrong-model', 'response-503', 'incomplete-response', 'request-hash', 'response-hash']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      if (failure === 'missing-proof') { f.value.transportFiles.pop(); f.save() }
      if (failure === 'duplicate-proof') { f.value.transportFiles.push({ ...f.value.transportFiles[0] }); f.save() }
      if (failure === 'changed-wire') appendFileSync(join(f.transportRoot, 'response-1.txt'), 'tampered after freeze')
      if (failure === 'wrong-origin') f.rewriteTransport('transport.jsonl', row => { row.upstreamURL = 'https://not-deepseek.invalid/anthropic/v1/messages?beta=true' })
      if (failure === 'wrong-model') {
        const request = JSON.parse(readFileSync(join(f.transportRoot, 'requests.jsonl'), 'utf8'))
        request.body.model = 'other-model'
        const raw = JSON.stringify(request.body)
        request.bodyHash = sha256(raw)
        const bodyProof = f.value.transportFiles.find(row => row.path === join(f.transportRoot, 'request-1.txt'))!
        Object.assign(bodyProof, f.write(bodyProof.path, raw))
        f.rewriteTransport('requests.jsonl', row => { Object.assign(row, request) })
        f.rewriteTransport('response-1.json', row => { row.requestHash = sha256(JSON.stringify(request)) })
      }
      if (failure === 'response-503') f.rewriteTransport('response-1.json', row => { row.status = 503 })
      if (failure === 'incomplete-response') f.rewriteTransport('response-1.json', row => { row.complete = false; row.error = 'synthetic interrupted stream' })
      if (failure === 'request-hash') f.rewriteTransport('requests.jsonl', row => { row.bodyHash = sha256('wrong request bytes') })
      if (failure === 'response-hash') f.rewriteTransport('response-1.json', row => { row.sha256 = sha256('wrong response bytes') })
      expect(f.verify).toThrow()
    }
  })
  test('Messages JSON needs actual type, stop reason, model, public id and valid usage', () => {
    for (const failure of ['missing-type', 'missing-stop', 'wrong-model', 'missing-id', 'negative-usage']) {
      const f = sourceParentFiles()
      expect(f.verify()).toBeTruthy()
      const message = JSON.parse(JSON.stringify((f.publicRows[0] as any).message))
      if (failure === 'missing-type') delete message.type
      if (failure === 'missing-stop') message.stop_reason = null
      if (failure === 'wrong-model') message.model = 'other-model'
      if (failure === 'missing-id') message.id = ''
      if (failure === 'negative-usage') message.usage.input_tokens = -1
      const raw = JSON.stringify(message)
      const proof = f.value.transportFiles.find(row => row.path === join(f.transportRoot, 'response-1.txt'))!
      Object.assign(proof, f.write(proof.path, raw))
      f.rewriteTransport('response-1.json', row => { row.contentType = 'application/json'; row.bytes = Buffer.byteLength(raw); row.sha256 = sha256(raw) })
      expect(f.verify).toThrow()
    }
  })
  test('observed stale parent evidence permanently invalidates the existing batch even after restoration', async () => {
    const f = sourceParentFiles(), batch = temporary()
    expect(f.verify()).toBeTruthy()
    writeFileSync(join(batch, 'manifest.json'), 'synthetic existing scored-batch manifest\n', { mode: 0o600 })
    const before = readFileSync(f.value.supervision.path)
    appendFileSync(f.value.supervision.path, 'tampered after startup')
    await expect(validateBatchStartup(batch, async () => f.verify())).rejects.toThrow()
    expect(existsSync(join(batch, 'invalid-batch.json'))).toBe(true)
    writeFileSync(f.value.supervision.path, before)
    await expect(validateBatchStartup(batch, async () => f.verify())).rejects.toThrow('permanently')
  })
})


describe('ordinary versioned final product authorization', () => {
  test('an explicitly versioned ordinary build binds frozen product version and all four assets', () => {
    const { root, value, receipt, binary, target } = candidateFiles({ productVersion: '0.7.1' })
    expect(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version).toBe('999.0.0-local')
    expect(JSON.parse(readFileSync(join(root, 'desktop/package.json'), 'utf8')).version).toBe('0.7.1')
    expect(value.build.argv.slice(1)).toEqual(['run', 'build:cli', '--version', '0.7.1'])
    expect(verifyCandidateEvidence(receipt.path, binary.sha256, target).inputsHash).toBe(value.inputs.sha256)
  })
  test('the original no-argument development build keeps its original qualification', () => {
    const { value, receipt, binary, target } = candidateFiles()
    expect(value.build.argv).toHaveLength(3)
    expect(value.assets.every(asset => !Object.hasOwn(asset, 'productVersion'))).toBe(true)
    expect(verifyCandidateEvidence(receipt.path, binary.sha256, target).binaryHash).toBe(binary.sha256)
  })
  test('only the exact already supported version option is admitted, without hidden build flags', () => {
    for (const tail of [['--version'], ['--version=0.7.1'], ['--version', '0.7.1', '--target', 'darwin/arm64'], ['--version', '0.7.1', '--version', '0.7.1'], ['--output-dir', '0.7.1'], ['--version', '0.7.1 -X secret=1'], ['--version', ''], ['--version', '0.7.2']]) {
      const { value, receipt, binary, target } = candidateFiles({ productVersion: '0.7.1' })
      value.build.argv = [value.bun.path, 'run', 'build:cli', ...tail]
      value.build.effectiveCommand = [...value.build.argv]
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow()
    }
  })
  test('the effective command cannot hide a different actual versioned build', () => {
    const { value, receipt, binary, target } = candidateFiles({ productVersion: '0.7.1' })
    value.build.effectiveCommand = [value.bun.path, 'run', 'build:cli']
    writeFileSync(receipt.path, JSON.stringify(value))
    expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('Actual pinned Bun command')
  })
  test('version buildinfo is an exact unique actual linker setting, not an arbitrary text marker', () => {
    for (const setting of ['', 'build\t-ldflags="-X main.version=0.7.10"\n', 'build\t-ldflags="-X main.version=0.7.1 -X other=value"\n', 'a comment containing -X main.version=0.7.1\n', 'build\t-ldflags="-X main.version=0.7.1"\nbuild\t-ldflags="-X main.version=0.7.1"\n']) {
      const { value, receipt, binary, target } = candidateFiles({ productVersion: '0.7.1' })
      const info = readFileSync(value.build.info.path, 'utf8').replace(/^build\t-ldflags=.+\n/m, '') + setting
      writeFileSync(value.build.info.path, info)
      value.build.info.sha256 = sha256(info)
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('product-version buildinfo')
    }
  })
  test('each final asset and its actual build receipt must bind the same frozen product release', () => {
    for (const role of ['react', 'native-host', 'native-helper', 'native-assets']) for (const mutation of ['asset-missing', 'asset-other', 'receipt-missing', 'receipt-other']) {
      const { value, receipt, binary, target } = candidateFiles({ productVersion: '0.7.1' })
      const asset = value.assets.find(row => row.role === role)!, built = JSON.parse(readFileSync(asset.receipt.path, 'utf8'))
      if (mutation === 'asset-missing') delete asset.productVersion
      if (mutation === 'asset-other') asset.productVersion = '0.7.2'
      if (mutation === 'receipt-missing') delete built.productVersion
      if (mutation === 'receipt-other') built.productVersion = '0.7.2'
      const text = JSON.stringify(built)
      writeFileSync(asset.receipt.path, text)
      asset.receipt.sha256 = sha256(text)
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow('Asset product version mismatch: ' + role)
    }
  })
  test('versioned products still require every original unfiltered quality lane', () => {
    for (const mode of ['skip', 'filter']) {
      const { value, receipt, binary, target, reportObject } = candidateFiles({ productVersion: '0.7.1' })
      if (mode === 'skip') reportObject.results[0].status = 'skipped'
      else {
        value.quality.argv.push('--only', 'go-checks')
        value.quality.effectiveCommand = [...value.quality.argv]
      }
      const report = JSON.stringify(reportObject)
      writeFileSync(value.quality.report.path, report)
      value.quality.report.sha256 = sha256(report)
      writeFileSync(receipt.path, JSON.stringify(value))
      expect(() => verifyCandidateEvidence(receipt.path, binary.sha256, target)).toThrow()
    }
  })
})

describe('explicit user-excluded Computer Use candidate scope', () => {
  const scopedFiles = () => {
    const fixture = candidateFiles({ productVersion: '0.7.1' })
    const value: any = fixture.value
    const report: any = fixture.reportObject
    value.scopeExclusions = ['computer-use']
    report.scopeExclusions = ['computer-use']
    report.results.find((row: any) => row.id === 'native-checks').scopeExclusions = ['computer-use']
    value.assets = value.assets.map((asset: any) => asset.role === 'native-helper'
      ? { role: 'native-helper', status: 'excluded', scope: 'computer-use', reason: 'user-requested', inputsHash: value.inputs.sha256 }
      : asset)
    const save = () => {
      const bytes = JSON.stringify(report)
      writeFileSync(value.quality.report.path, bytes)
      value.quality.report.sha256 = sha256(bytes)
      writeFileSync(fixture.receipt.path, JSON.stringify(value))
    }
    save()
    return { ...fixture, value, report, save }
  }
  test('an explicit user-excluded CU helper with matching actual full quality scope is admitted', () => {
    const f = scopedFiles()
    expect(f.report.results).toHaveLength(lanesForMode('pr').filter(lane => !lane.live).length)
    expect(f.report.results.every((row: any) => row.status === 'passed' && row.exitCode === 0)).toBe(true)
    expect(f.value.assets.find((asset: any) => asset.role === 'native-helper')).toEqual({ role: 'native-helper', status: 'excluded', scope: 'computer-use', reason: 'user-requested', inputsHash: f.value.inputs.sha256 })
    expect(verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target).inputsHash).toBe(f.value.inputs.sha256)
  })
  test('default evidence still requires all four actual successful asset roles', () => {
    const f = candidateFiles({ productVersion: '0.7.1' })
    expect(Object.hasOwn(f.value, 'scopeExclusions')).toBe(false)
    expect(verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target).binaryHash).toBe(f.binary.sha256)
    f.value.assets = f.value.assets.filter(asset => asset.role !== 'native-helper')
    writeFileSync(f.receipt.path, JSON.stringify(f.value))
    expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Missing final candidate asset: native-helper')
  })
  test('unknown, broad, duplicate or malformed scope cannot authorize exclusions', () => {
    for (const scope of [[], ['native'], ['computer-use', 'computer-use'], ['computer-use', 'go-checks'], 'computer-use', null, {}, [true]]) {
      const f = scopedFiles()
      f.value.scopeExclusions = scope
      f.save()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Unsupported candidate scope exclusion')
    }
  })
  test('the actual quality report and native result must match the candidate scope', () => {
    for (const field of ['report', 'native']) for (const scope of [undefined, ['native'], ['computer-use', 'go-checks']]) {
      const f = scopedFiles()
      if (field === 'report') f.report.scopeExclusions = scope
      else f.report.results.find((row: any) => row.id === 'native-checks').scopeExclusions = scope
      f.save()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
  })
  test('an undisclosed native omission is rejected without explicit candidate scope', () => {
    const f = scopedFiles()
    delete f.value.scopeExclusions
    f.save()
    expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Candidate quality scope exclusion mismatch')
  })
  test('scope cannot skip, fail, remove, substitute or filter any required lane', () => {
    for (const change of ['native-skip', 'native-fail', 'native-missing', 'native-command', 'go-skip', 'coverage-fail', 'verify-filter']) {
      const f = scopedFiles()
      const native = f.report.results.find((row: any) => row.id === 'native-checks')
      if (change === 'native-skip') native.status = 'skipped'
      if (change === 'native-fail') native.exitCode = 1
      if (change === 'native-missing') f.report.results = f.report.results.filter((row: any) => row.id !== 'native-checks')
      if (change === 'native-command') native.command = ['bun', 'run', 'check:electron']
      if (change === 'go-skip') f.report.results.find((row: any) => row.id === 'go-checks').status = 'skipped'
      if (change === 'coverage-fail') f.report.results.find((row: any) => row.id === 'coverage').status = 'failed'
      if (change === 'verify-filter') {
        f.value.quality.argv.push('--skip', 'native-checks')
        f.value.quality.effectiveCommand = [...f.value.quality.argv]
      }
      f.save()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
  })
  test('excluded helper is a truthful single scoped record, never fabricated build success', () => {
    for (const change of ['missing', 'duplicate', 'status', 'scope', 'reason', 'inputs', 'files', 'receipt', 'exit', 'version']) {
      const f = scopedFiles()
      const helper = f.value.assets.find((asset: any) => asset.role === 'native-helper')
      if (change === 'missing') f.value.assets = f.value.assets.filter((asset: any) => asset !== helper)
      if (change === 'duplicate') f.value.assets.push({ ...helper })
      if (change === 'status') helper.status = 'passed'
      if (change === 'scope') helper.scope = 'native'
      if (change === 'reason') helper.reason = 'missing-XCTest'
      if (change === 'inputs') helper.inputsHash = sha256('another input epoch')
      if (change === 'files') helper.files = []
      if (change === 'receipt') helper.receipt = f.receipt
      if (change === 'exit') helper.exitCode = 0
      if (change === 'version') helper.productVersion = '0.7.2'
      f.save()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
  })
  test('remaining actual native assets, release versions and all raw logs stay mandatory', () => {
    for (const role of ['react', 'native-host', 'native-assets']) for (const change of ['missing', 'stale', 'version', 'receipt-version']) {
      const f = scopedFiles()
      const asset = f.value.assets.find((row: any) => row.role === role)
      if (change === 'missing') f.value.assets = f.value.assets.filter((row: any) => row !== asset)
      if (change === 'stale') appendFileSync(asset.files[0].path, 'changed actual bytes')
      if (change === 'version') asset.productVersion = '0.7.2'
      if (change === 'receipt-version') {
        const receipt = JSON.parse(readFileSync(asset.receipt.path, 'utf8'))
        receipt.productVersion = '0.7.2'
        const bytes = JSON.stringify(receipt)
        writeFileSync(asset.receipt.path, bytes)
        asset.receipt.sha256 = sha256(bytes)
      }
      f.save()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
    const f = scopedFiles()
    appendFileSync(f.value.quality.logs.find((row: any) => row.path.endsWith('/native-checks.log')).path, 'changed retained native log')
    expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Evidence file changed')
  })
})

// Unchanged real Go1.25.7 tool output from the ordinary 0.7.1 CLI build.
// The surrounding receipts below are isolated validator fixtures, not build success.
const ACTUAL_GO125_TRIMPATH_INFO = "/private/tmp/cch-product-0_7_1-o3_5rt_g/repository/bin/native/claude-haha-darwin-arm64: go1.25.7\n\tpath\tgithub.com/NanmiCoder/cc-haha/backend/cmd/claude-haha\n\tmod\tgithub.com/NanmiCoder/cc-haha/backend\t(devel)\t\n\tdep\tcharm.land/bubbletea/v2\tv2.0.9\th1:DpJCMWKgzQK8SJv4zbKKFHAI10ymWy/evClPFk0k0f8=\n\tdep\tfilippo.io/edwards25519\tv1.1.0\th1:FNf4tywRC1HmFuKW5xopWpigGjJKiJSV0Cqo0cJWDaA=\n\tdep\tgithub.com/andybalholm/brotli\tv1.2.0\th1:ukwgCxwYrmACq68yiUqwIWnGY0cTPox/M94sVwToPjQ=\n\tdep\tgithub.com/beeper/argo-go\tv1.1.2\th1:UQI2G8F+NLfGTOmTUI0254pGKx/HUU/etbUGTJv91Fs=\n\tdep\tgithub.com/bits-and-blooms/bitset\tv1.24.4\th1:95H15Og1clikBrKr/DuzMXkQzECs1M6hhoGXLwLQOZE=\n\tdep\tgithub.com/charmbracelet/colorprofile\tv0.4.3\th1:QPa1IWkYI+AOB+fE+mg/5/4HRMZcaXex9t5KX76i20Q=\n\tdep\tgithub.com/charmbracelet/ultraviolet\tv0.0.0-20260703014108-f5a850f9c2b7\th1:3FmWoGNWK4STvqg0O0Aeav2T7rodWJAPeF0QpH+8gFw=\n\tdep\tgithub.com/charmbracelet/x/ansi\tv0.11.7\th1:kzv1kJvjg2S3r9KHo8hDdHFQLEqn4RBCb39dAYC84jI=\n\tdep\tgithub.com/charmbracelet/x/term\tv0.2.2\th1:xVRT/S2ZcKdhhOuSP4t5cLi5o+JxklsoEObBSgfgZRk=\n\tdep\tgithub.com/charmbracelet/x/termios\tv0.1.1\th1:o3Q2bT8eqzGnGPOYheoYS8eEleT5ZVNYNy8JawjaNZY=\n\tdep\tgithub.com/charmbracelet/x/windows\tv0.2.2\th1:IofanmuvaxnKHuV04sC0eBy/smG6kIKrWG2/jYn2GuM=\n\tdep\tgithub.com/clipperhouse/displaywidth\tv0.11.0\th1:lBc6kY44VFw+TDx4I8opi/EtL9m20WSEFgwIwO+UVM8=\n\tdep\tgithub.com/clipperhouse/uax29/v2\tv2.7.0\th1:+gs4oBZ2gPfVrKPthwbMzWZDaAFPGYK72F0NJv2v7Vk=\n\tdep\tgithub.com/coder/websocket\tv1.8.14\th1:9L0p0iKiNOibykf283eHkKUHHrpG7f65OE3BhhO7v9g=\n\tdep\tgithub.com/dlclark/regexp2/v2\tv2.8.1\th1:I1jbaCbXeOxdVN8z3fb55Mvtgb94/6+Dh0zjmXjKV/0=\n\tdep\tgithub.com/dop251/goja\tv0.0.0-20261002135814-104bc28c3abd\th1:4doKDl9pw2k9zw1h502dLnnL9EbOC80D6aUXbKujMsI=\n\tdep\tgithub.com/dustin/go-humanize\tv1.0.1\th1:GzkhY7T5VNhEkwH0PVJgjz+fX1rhBrR7pRT3mDkpeCY=\n\tdep\tgithub.com/ebitengine/purego\tv0.9.1\th1:a/k2f2HQU3Pi399RPW1MOaZyhKJL9w/xFpKAg4q1s0A=\n\tdep\tgithub.com/elliotchance/orderedmap/v3\tv3.1.0\th1:j4DJ5ObEmMBt/lcwIecKcoRxIQUEnw0L804lXYDt/pg=\n\tdep\tgithub.com/fsnotify/fsnotify\tv1.10.1\th1:b0/UzAf9yR5rhf3RPm9gf3ehBPpf0oZKIjtpKrx59Ho=\n\tdep\tgithub.com/go-sourcemap/sourcemap\tv2.1.3+incompatible\th1:W1iEw64niKVGogNgBN3ePyLFfuisuzeidWPMPWmECqU=\n\tdep\tgithub.com/google/pprof\tv0.0.0-20250317173921-a4b03ec1a45e\th1:ijClszYn+mADRFY17kjQEVQ1XRhq2/JR1M3sGqeJoxs=\n\tdep\tgithub.com/google/uuid\tv1.6.0\th1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=\n\tdep\tgithub.com/klauspost/compress\tv1.20.1\th1:T7kKElXUMXrUJ2E9QhQhxFtcK5rPyLdsGZvdbLMPdiQ=\n\tdep\tgithub.com/lucasb-eyer/go-colorful\tv1.4.0\th1:UtrWVfLdarDgc44HcS7pYloGHJUjHV/4FwW4TvVgFr4=\n\tdep\tgithub.com/mattn/go-colorable\tv0.1.14\th1:9A9LHSqF/7dyVVX6g0U9cwm9pG3kP9gSzcuIPHPsaIE=\n\tdep\tgithub.com/mattn/go-isatty\tv0.0.20\th1:xfD0iDuEKnDkl03q4limB+vH+GxLEtL/jb4xVJSWWEY=\n\tdep\tgithub.com/mattn/go-runewidth\tv0.0.23\th1:7ykA0T0jkPpzSvMS5i9uoNn2Xy3R383f9HDx3RybWcw=\n\tdep\tgithub.com/muesli/cancelreader\tv0.2.2\th1:3I4Kt4BQjOR54NavqnDogx/MIoWBFa0StPA8ELUXHmA=\n\tdep\tgithub.com/ncruces/go-strftime\tv1.0.0\th1:HMFp8mLCTPp341M/ZnA4qaf7ZlsbTc+miZjCLOFAw7w=\n\tdep\tgithub.com/nlnwa/whatwg-url\tv0.6.2\th1:jU61lU2ig4LANydbEJmA2nPrtCGiKdtgT0rmMd2VZ/Q=\n\tdep\tgithub.com/remyoudompheng/bigfft\tv0.0.0-20230129092748-24d4a6f8daec\th1:W09IVJc94icq4NjY3clb7Lk8O1qJ8BdBEF8z0ibU0rE=\n\tdep\tgithub.com/rivo/uniseg\tv0.4.7\th1:WUdvkW8uEhrYfLC4ZzdpI2ztxP1I582+49Oc5Mq64VQ=\n\tdep\tgithub.com/rs/zerolog\tv1.34.0\th1:k43nTLIwcTVQAncfCw4KZ2VY6ukYoZaBPNOE8txlOeY=\n\tdep\tgithub.com/skip2/go-qrcode\tv0.0.0-20200617195104-da1b6568686e\th1:MRM5ITcdelLK2j1vwZ3Je0FKVCfqOLp5zO6trqMLYs0=\n\tdep\tgithub.com/spf13/cobra\tv1.10.2\th1:DMTTonx5m65Ic0GOoRY2c16WCbHxOOw6xxezuLaBpcU=\n\tdep\tgithub.com/spf13/pflag\tv1.0.9\th1:9exaQaMOCwffKiiiYk6/BndUBv+iRViNW+4lEMi0PvY=\n\tdep\tgithub.com/vektah/gqlparser/v2\tv2.5.27\th1:RHPD3JOplpk5mP5JGX8RKZkt2/Vwj/PZv0HxTdwFp0s=\n\tdep\tgithub.com/xo/terminfo\tv0.0.0-20220910002029-abceb7e1c41e\th1:JVG44RsyaB9T2KIHavMF/ppJZNG9ZpyihvCd0w101no=\n\tdep\tgithub.com/yuin/goldmark\tv1.7.13\th1:GPddIs617DnBLFFVJFgpo1aBfe/4xcvMc3SB5t/D0pA=\n\tdep\tgo.mau.fi/libsignal\tv0.2.1\n\t=>\t./third_party/libsignal-node\t(devel)\t\n\t\n\tdep\tgo.mau.fi/util\tv0.9.6\th1:2nsvxm49KhI3wrFltr0+wSUBlnQ4CMtykuELjpIU+ts=\n\tdep\tgo.mau.fi/whatsmeow\tv0.0.0-20260218135554-9cbe80fb25a4\n\t=>\t./third_party/whatsmeow-node\t(devel)\t\n\t\n\tdep\tgo.yaml.in/yaml/v3\tv3.0.4\th1:tfq32ie2Jv2UxXFdLJdh3jXuOzWiL1fo0bu/FbuKpbc=\n\tdep\tgolang.org/x/crypto\tv0.48.0\th1:/VRzVqiRSggnhY7gNRxPauEQ5Drw9haKdM0jqfcCFts=\n\tdep\tgolang.org/x/net\tv0.50.0\th1:ucWh9eiCGyDR3vtzso0WMQinm2Dnt8cFMuQa9K33J60=\n\tdep\tgolang.org/x/sync\tv0.21.0\th1:HLII4xRRTtCRkxYp4HNFF0Js/Og6q2i++KXbg0gHCwM=\n\tdep\tgolang.org/x/sys\tv0.46.0\th1:noSf2Fq6F8DBgS+LysIkx7rIExoNHJsxOAtPp4rthXw=\n\tdep\tgolang.org/x/text\tv0.34.0\th1:oL/Qq0Kdaqxa1KbNeMKwQq0reLCCaFtqu2eNuSeNHbk=\n\tdep\tgoogle.golang.org/protobuf\tv1.36.11\th1:fV6ZwhNocDyBLK0dj+fg8ektcVegBBuEolpbTQyBNVE=\n\tdep\tmodernc.org/libc\tv1.73.4\th1:+ra4Ui8ngyt8HDcO1FTDPWlkAh6yOdaO2yAoh8MddQA=\n\tdep\tmodernc.org/mathutil\tv1.7.1\th1:GCZVGXdaN8gTqB1Mf/usp1Y/hSqgI2vAGGP4jZMCxOU=\n\tdep\tmodernc.org/memory\tv1.11.0\th1:o4QC8aMQzmcwCK3t3Ux/ZHmwFPzE6hf2Y5LbkRs+hbI=\n\tdep\tmodernc.org/sqlite\tv1.53.0\th1:20WG8N9q4ji/dEqGk4uiI0c6OPjSeLTNYGFCc3+7c1M=\n\tbuild\t-buildmode=exe\n\tbuild\t-compiler=gc\n\tbuild\t-trimpath=true\n\tbuild\tCGO_ENABLED=0\n\tbuild\tGOARCH=arm64\n\tbuild\tGOOS=darwin\n\tbuild\tGOARM64=v8.0\n\tbuild\tvcs=git\n\tbuild\tvcs.revision=f7599e7efdc28f6fc5e07fc2d903cdd67135b90f\n\tbuild\tvcs.time=2026-10-13T12:40:02Z\n\tbuild\tvcs.modified=true\n"

function trimpathVersionCandidate(noEnvFile = false) {
  const fixture = candidateFiles({ productVersion: '0.7.1' })
  const value = fixture.value as any
  const file = (name: string, text: string) => {
    const path = join(temporary(), name)
    writeFileSync(path, text, { mode: 0o600 })
    return { path, sha256: sha256(text) }
  }
  // Preserve the actual BuildInfo bytes, including its actual darwin/arm64 target.
  value.target = 'darwin/arm64'
  value.build.info = file('actual-buildinfo.txt', ACTUAL_GO125_TRIMPATH_INFO)
  value.build.argv = [value.bun.path, ...(noEnvFile ? ['--no-env-file'] : []), 'run', 'build:cli', '--version', '0.7.1']
  value.build.effectiveCommand = [...value.build.argv]
  const built = { argv: [...value.build.argv], cwd: value.root, exitCode: 0, timedOut: false }
  value.build.receipt = file('ordinary-build.json', JSON.stringify(built))
  const probe = {
    kind: 'ordinary-cli-public-version-probe', version: 1,
    argv: [value.binary.path, '--version'], cwd: value.root,
    exitCode: 0, signal: null, timedOut: false,
    binaryHashBefore: value.binary.sha256, binaryHashAfter: value.binary.sha256,
    inputsBefore: value.inputs.sha256, inputsAfter: value.inputs.sha256,
    ownedGroupRemaining: [], stdout: file('version-stdout.txt', '0.7.1 (Claude Code)\n'), stderr: file('version-stderr.txt', ''),
  }
  value.build.versionProbe = file('version-probe.json', JSON.stringify(probe))
  const write = () => writeFileSync(fixture.receipt.path, JSON.stringify(value))
  const writeProbe = () => {
    writeFileSync(value.build.versionProbe.path, JSON.stringify(probe))
    value.build.versionProbe.sha256 = sha256(readFileSync(value.build.versionProbe.path))
    write()
  }
  const writeBuilt = () => {
    writeFileSync(value.build.receipt.path, JSON.stringify(built))
    value.build.receipt.sha256 = sha256(readFileSync(value.build.receipt.path))
    write()
  }
  write()
  return { ...fixture, value, target: 'darwin/arm64', built, probe, write, writeProbe, writeBuilt }
}

describe('actual Go1.25 trimpath version evidence', () => {
  test('actual Go1.25 trimpath metadata binds an ordinary versioned build and same-binary public output', () => {
    const f = trimpathVersionCandidate()
    expect(readFileSync(f.value.build.info.path, 'utf8')).toBe(ACTUAL_GO125_TRIMPATH_INFO)
    expect(ACTUAL_GO125_TRIMPATH_INFO).not.toContain('build\t-ldflags=')
    expect(verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target).binaryHash).toBe(f.binary.sha256)
  })
  test('the optional no-env-file flag is exact and the original argv is authenticated', () => {
    const allowed = trimpathVersionCandidate(true)
    expect(verifyCandidateEvidence(allowed.receipt.path, allowed.binary.sha256, allowed.target).binaryHash).toBe(allowed.binary.sha256)
    for (const flag of ['--env-file=secret', '--preload', '--no-env-file --smol']) {
      const f = trimpathVersionCandidate(true)
      f.value.build.argv[1] = flag
      f.value.build.effectiveCommand = [...f.value.build.argv]
      f.built.argv = [...f.value.build.argv]
      f.writeBuilt()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Actual pinned Bun command')
    }
  })
  test('missing actual receipts and mismatched build commands cannot use trimpath omission', () => {
    for (const mutation of ['missing-build', 'missing-probe', 'wrong-cwd', 'failed-build', 'timeout-build', 'extra-build-arg']) {
      const f = trimpathVersionCandidate()
      if (mutation === 'missing-build') delete f.value.build.receipt
      if (mutation === 'missing-probe') delete f.value.build.versionProbe
      if (mutation === 'wrong-cwd') f.built.cwd += '/other'
      if (mutation === 'failed-build') f.built.exitCode = 1
      if (mutation === 'timeout-build') f.built.timedOut = true
      if (mutation === 'extra-build-arg') f.built.argv.push('--target', 'darwin/arm64')
      if (!mutation.startsWith('missing')) f.writeBuilt()
      else f.write()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
  })
  test('public probe binds exact binary, argv, inputs, natural terminal and empty owned group', () => {
    for (const mutation of ['wrong-before-hash', 'wrong-after-hash', 'wrong-input-before', 'wrong-input-after', 'wrong-binary-path', 'extra-probe-arg', 'wrong-cwd', 'wrong-kind', 'wrong-schema', 'failure', 'signal', 'timeout', 'remaining']) {
      const f = trimpathVersionCandidate()
      if (mutation === 'wrong-before-hash') f.probe.binaryHashBefore = '0'.repeat(64)
      if (mutation === 'wrong-after-hash') f.probe.binaryHashAfter = '0'.repeat(64)
      if (mutation === 'wrong-input-before') f.probe.inputsBefore = '0'.repeat(64)
      if (mutation === 'wrong-input-after') f.probe.inputsAfter = '0'.repeat(64)
      if (mutation === 'wrong-binary-path') f.probe.argv[0] += '.other'
      if (mutation === 'extra-probe-arg') f.probe.argv.push('--help')
      if (mutation === 'wrong-cwd') f.probe.cwd += '/other'
      if (mutation === 'wrong-kind') f.probe.kind = 'unrelated'
      if (mutation === 'wrong-schema') f.probe.version = 2
      if (mutation === 'failure') f.probe.exitCode = 1
      if (mutation === 'signal') (f.probe as any).signal = 'SIGTERM'
      if (mutation === 'timeout') f.probe.timedOut = true
      if (mutation === 'remaining') (f.probe.ownedGroupRemaining as number[]).push(123)
      f.writeProbe()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('same-binary public version probe')
    }
  })
  test('version output must be exact and every probe raw is rehashed at each boundary', () => {
    for (const output of ['0.7.10 (Claude Code)\n', '0.7.1\n', '0.7.1 (Claude Code)\nextra', '']) {
      const f = trimpathVersionCandidate()
      writeFileSync(f.probe.stdout.path, output)
      f.probe.stdout.sha256 = sha256(output)
      f.writeProbe()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('public version output mismatch')
    }
    const f = trimpathVersionCandidate()
    expect(verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target).binaryHash).toBe(f.binary.sha256)
    appendFileSync(f.probe.stdout.path, 'changed')
    expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow('Evidence file changed')
    const error = trimpathVersionCandidate()
    writeFileSync(error.probe.stderr.path, 'warning')
    error.probe.stderr.sha256 = sha256('warning')
    error.writeProbe()
    expect(() => verifyCandidateEvidence(error.receipt.path, error.binary.sha256, error.target)).toThrow('public version output mismatch')
  })
  test('nontrimpath binaries and contradictory linker flags retain their original rejection', () => {
    for (const metadata of [ACTUAL_GO125_TRIMPATH_INFO.replace('build\t-trimpath=true\n', ''), ACTUAL_GO125_TRIMPATH_INFO + 'build\t-ldflags="-X main.version=0.7.10"\n']) {
      const f = trimpathVersionCandidate()
      writeFileSync(f.value.build.info.path, metadata)
      f.value.build.info.sha256 = sha256(metadata)
      f.write()
      expect(() => verifyCandidateEvidence(f.receipt.path, f.binary.sha256, f.target)).toThrow()
    }
  })
})
