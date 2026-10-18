import { appendFileSync as nativeAppendFileSync, writeFileSync as nativeWriteFileSync } from 'node:fs'
import { PAIRED_PROTOCOL, sha256 } from '../../../scripts/perf/go-main-paired-benchmark-core'
import { createStreamEvidence } from '../../../scripts/perf/go-main-paired-benchmark-evidence'

export function startParentTransport(path: string, key: string) {
  const state = { path, route: '', calls: 0, writes: [] as Promise<void>[], controllers: new Set<AbortController>(), errors: [] as string[], transportErrors: [] as string[], recordingErrors: [] as string[], modelCalls: 0, closing: false, redactions: 0 }
  let active: typeof state | undefined = state
  const redact = (value: string) => value.replaceAll(key, '<redacted-key>')
  const sanitize = (value: string | Uint8Array) => {
    const bytes = typeof value === 'string' ? Buffer.from(value) : Buffer.from(value)
    const needle = Buffer.from(key)
    if (bytes.indexOf(needle) < 0) return value
    state.redactions++
    // Redaction makes this pilot unfreezable; retain a safe diagnostic copy.
    return Buffer.from(bytes.toString('utf8').replaceAll(key, '<redacted-key>'))
  }
  const writeFileSync: any = (path: any, data: any, ...options: any[]) => (nativeWriteFileSync as any)(path, sanitize(data), ...options)
  const appendFileSync: any = (path: any, data: any, ...options: any[]) => (nativeAppendFileSync as any)(path, sanitize(data), ...options)
const upstream = Bun.serve({ hostname: '127.0.0.1', port: 0, development: false, async fetch(request) {
    if (!active) return new Response('No active benchmark task', { status: 503 })
    const capture = active
    const url = new URL(request.url)
    if (!(request.method === 'HEAD' && url.pathname === capture.route + '/anthropic') && !url.pathname.startsWith(capture.route + '/anthropic/')) return new Response('Retired or unexpected benchmark task', { status: 410 })
    const pathname = url.pathname.slice(capture.route.length)
    const controller = new AbortController()
    capture.controllers.add(controller)
    let finished = false
    let number: number | undefined
    let requestHash: string | undefined
    let phase: 'body' | 'fetch' | 'stream' = 'body'
    let finishRequest!: () => void
    const owned = new Promise<void>(resolve => { finishRequest = resolve })
    capture.writes.push(owned)
    const finish = () => {
      if (finished) return
      finished = true
      clearTimeout(requestTimer)
      request.signal.removeEventListener('abort', propagateAbort)
      controller.signal.removeEventListener('abort', canceled)
      capture.controllers.delete(controller)
      finishRequest()
    }
    const propagateAbort = () => controller.abort(new Error('Benchmark HTTP consumer canceled'))
    const transportFailure = (error: string) => {
      if (request.method !== 'HEAD') capture.errors.push(redact(error))
      // Source preconnect is fire-and-forget and catches transport rejection.
      // Keep its real failure separate from a billable model stream failure.
      if (request.method === 'HEAD') capture.transportErrors.push(redact(error))
      if (number !== undefined) {
        try {
          if (request.method !== 'HEAD') writeFileSync(capture.path + `/response-${number}.txt`, new Uint8Array(), { mode: 0o600 })
          writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: 0, complete: false, error: redact(error), bytes: 0, sha256: sha256(new Uint8Array()), requestHash }), { mode: 0o600 })
        } catch (failure) { capture.recordingErrors.push('Evidence write failed: ' + String(failure)) }
      }
    }
    const canceled = () => {
      if (phase === 'stream') return // Evidence settles and writes its partial body.
      transportFailure(phase === 'fetch' ? 'Upstream fetch failed: owned request canceled before headers' : 'Owned request canceled before body admission')
      finish()
    }
    const requestTimer = setTimeout(() => controller.abort(new Error('Benchmark owned request timeout')), PAIRED_PROTOCOL.timeoutMs + 5_000)
    request.signal.addEventListener('abort', propagateAbort, { once: true })
    controller.signal.addEventListener('abort', canceled, { once: true })
    let body: string | undefined
    let bodyBytes: Uint8Array | undefined
    try {
      if (request.method !== 'GET' && request.method !== 'HEAD') {
        bodyBytes = new Uint8Array(await request.arrayBuffer())
        body = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bodyBytes)
      }
    } catch (error) {
      capture.errors.push(redact('Request body failed: ' + String(error)))
      finish()
      return new Response('Invalid benchmark request body', { status: 400 })
    }
    if (active !== capture || capture.closing || controller.signal.aborted) {
      finish()
      return new Response('Retired benchmark body admission', { status: 410 })
    }
    number = ++capture.calls
    if (url.pathname.endsWith('/messages')) {
      try {
        const captured = { number, method: request.method, pathname, query: url.search, bodyHash: sha256(bodyBytes!), body: JSON.parse(body!) }
        const serialized = redact(JSON.stringify(captured))
        requestHash = sha256(serialized)
        capture.modelCalls++
        writeFileSync(capture.path + `/request-${number}.txt`, bodyBytes!, { mode: 0o600 })
        appendFileSync(capture.path + '/requests.jsonl', serialized + '\n', { mode: 0o600 })
      } catch (error) {
        capture.recordingErrors.push(redact('Request evidence failed: ' + String(error)))
        finish()
        return new Response('Invalid benchmark request evidence', { status: 400 })
      }
    }
    const upstreamURL = 'https://api.deepseek.com' + pathname + url.search
    try { appendFileSync(capture.path + '/transport.jsonl', JSON.stringify({ number, method: request.method, pathname, query: url.search, upstreamURL }) + '\n', { mode: 0o600 }) } catch (error) {
      capture.recordingErrors.push('Transport evidence write failed: ' + String(error)); finish()
      return new Response('Invalid benchmark transport evidence', { status: 500 })
    }
    const headers = new Headers(request.headers)
    for (const name of ['host', 'connection', 'content-length']) headers.delete(name)
    phase = 'fetch'
    let response: Response
    try {
      response = await fetch(upstreamURL, { method: request.method, headers, body: bodyBytes, signal: controller.signal, redirect: 'error' })
    } catch (error) {
      if (!finished) transportFailure('Upstream fetch failed: ' + String(error))
      finish()
      return new Response('Benchmark upstream transport failed', { status: 502 })
    }
    if (active !== capture || capture.closing || controller.signal.aborted || finished) {
      controller.abort(new Error('Retired benchmark upstream response'))
      // A fetch implementation may deliver headers after cancellation. Its
      // body must not acquire a new recorder or escape the retired owner.
      if (response.body) void response.body.cancel().catch(() => {})
      finish()
      return new Response('Retired benchmark response', { status: 410 })
    }
    const outputHeaders = new Headers(response.headers)
    // Bun fetch decodes gzip. Preserve the actual bytes consumed by both SDKs.
    for (const name of ['content-encoding', 'content-length']) outputHeaders.delete(name)
    if (!response.body || request.method === 'HEAD') {
      try {
        if (request.method !== 'HEAD') writeFileSync(capture.path + `/response-${number}.txt`, new Uint8Array(), { mode: 0o600 })
        writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: response.status, complete: true, bytes: 0, sha256: sha256(new Uint8Array()), contentType: response.headers.get('content-type') ?? '', requestHash }), { mode: 0o600 })
      } catch (error) { capture.recordingErrors.push('Evidence write failed: ' + String(error)) }
      if (request.method === 'HEAD' && response.body) void response.body.cancel().catch(() => {})
      finish()
      return new Response(null, { status: response.status, headers: outputHeaders })
    }
    phase = 'stream'
    const recorded = createStreamEvidence(response.body, { signal: request.signal, abortController: controller, timeoutMs: PAIRED_PROTOCOL.timeoutMs + 5_000 })
    void recorded.completion.then(result => {
      try {
        writeFileSync(capture.path + `/response-${number}.txt`, result.partialBytes, { mode: 0o600 })
        writeFileSync(capture.path + `/response-${number}.json`, JSON.stringify({ method: request.method, status: response.status, complete: result.complete, error: result.error, bytes: result.partialBytes.length, sha256: sha256(result.partialBytes), contentType: response.headers.get('content-type') ?? '', requestHash }), { mode: 0o600 })
      } catch (error) { capture.recordingErrors.push('Evidence write failed: ' + String(error)) }
      if (!result.complete) capture.errors.push(redact('Incomplete stream evidence: ' + result.error))
      finish()
    })
    return new Response(recorded.consumer, { status: response.status, headers: outputHeaders })
  } })

  return {
    origin: upstream.url.origin,
    async close() {
      state.closing = true
      for (const controller of state.controllers) controller.abort(new Error('Parent pilot CLI exited; close owned request'))
      await Promise.all(state.writes)
      active = undefined
      await upstream.stop(true)
    },
    snapshot() { return { calls: state.calls, modelCalls: state.modelCalls, errors: state.errors, transportErrors: state.transportErrors, recordingErrors: state.recordingErrors, redactions: state.redactions } },
  }
}
