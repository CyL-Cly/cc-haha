import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native OpenAI compression')
if (process.env.CC_HAHA_OPENAI_COMPRESSION_FIXTURE !== '1') throw new Error('Unowned OpenAI compression fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { encodeOpenAIRequestBody } = await import('../../../src/services/openaiAuth/requestCompression')
const { getRequestBodyAudit } = await import('../../../src/services/api/requestBodyAudit')
const original = Bun.zstdCompress
const output = []
for (const row of input.rows) {
  process.env.CC_HAHA_OPENAI_REQUEST_COMPRESSION = row.optOut
  const controller = new AbortController()
  const reason = new Error('fixture-abort-reason')
  if (row.abortBefore) controller.abort(reason)
  const calls: unknown[] = []
  Object.assign(Bun, { zstdCompress: row.unsupported ? undefined : async (bytes: Uint8Array, options: unknown) => {
    calls.push(options)
    if (row.abortAfter) controller.abort(reason)
    if (row.encoderFailure) throw new Error('fixture-local-encoder-failure')
    return original(bytes, options as any)
  } })
  const headers = new Headers(row.headers)
  try {
    const wire = await encodeOpenAIRequestBody(row.plain, headers, controller.signal)
    const audit = getRequestBodyAudit(wire)
    output.push({
      kind: typeof wire === 'string' ? 'plain' : 'encoded',
      decoded: typeof wire === 'string' ? wire : Buffer.from(Bun.zstdDecompressSync(wire)).toString('utf8'),
      headers: Object.fromEntries(headers),
      calls,
      audit: audit ? { plainBody: audit.plainBody, requestEncoding: audit.requestEncoding, requestPlainBytes: audit.requestPlainBytes } : null,
      foreignDecoded: row.foreignWire ? Buffer.from(Bun.zstdDecompressSync(Buffer.from(row.foreignWire, 'base64'))).toString('utf8') : null,
    })
  } catch (error) {
    output.push({ error: (error as Error).message, calls })
  } finally {
    Object.assign(Bun, { zstdCompress: original })
  }
}
process.stdout.write(JSON.stringify(output))
