import { assertDisposableFixtureHome } from './fixture-guard'
import { openaiChatStreamToAnthropic } from '../../../src/server/proxy/streaming/openaiChatStreamToAnthropic'

assertDisposableFixtureHome(process.env.HOME, 'Provider Chat stream')
Date.now = () => 1777000000123
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text()) as Array<{ input: string, bytes?: number[], chunkSize: number }>
const results = []
for (const row of rows) {
  const bytes = row.bytes ? new Uint8Array(row.bytes) : new TextEncoder().encode(row.input)
  let offset = 0
  const cancelled: unknown[] = []
  const upstream = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (offset === bytes.length) return controller.close()
      controller.enqueue(bytes.slice(offset, offset + row.chunkSize))
      offset = Math.min(bytes.length, offset + row.chunkSize)
    },
    cancel(reason) { cancelled.push(reason ?? null) },
  })
  const frames = []
  const reader = openaiChatStreamToAnthropic(upstream, 'fixture-model').getReader()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    frames.push(new TextDecoder().decode(value))
  }
  results.push({ frames, cancelled })
}
process.stdout.write(JSON.stringify(results))
