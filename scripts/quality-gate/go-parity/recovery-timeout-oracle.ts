import Anthropic from '@anthropic-ai/sdk'

export type TimeoutObservation = {
  input: string
  parsed: string
  clientTimeout: string
  scheduled: string[]
  fired: string[]
  fetchCalls: number
  aborts: number
  outcome: { name: string; message: string } | { text: string }
  // Timing is recorded as diagnostic metadata, never used as a parity value.
  elapsedMs: number
}

/** Run in a disposable child: this observes the real SDK's timer, not a fake. */
export async function observeRecoveryTimeout(input: string, baseURL: string): Promise<TimeoutObservation> {
  const parsed = parseInt(input || String(600_000), 10)
  const observation: TimeoutObservation = {
    input, parsed: String(parsed), clientTimeout: '', scheduled: [], fired: [],
    fetchCalls: 0, aborts: 0, outcome: { text: '' }, elapsedMs: 0,
  }
  const realSetTimeout = globalThis.setTimeout
  const start = performance.now()
  globalThis.setTimeout = ((handler: (...args: unknown[]) => void, delay?: number, ...args: unknown[]) => {
    observation.scheduled.push(String(delay))
    return realSetTimeout(() => {
      observation.fired.push(String(delay))
      handler(...args)
    }, delay)
  }) as typeof setTimeout
  try {
    const client = new Anthropic({
      apiKey: 'fixture-key', baseURL, timeout: parsed, maxRetries: 0,
      fetch: (url, init) => {
        observation.fetchCalls++
        init?.signal?.addEventListener('abort', () => { observation.aborts++ }, { once: true })
        return fetch(url, init)
      },
    })
    observation.clientTimeout = String(client.timeout)
    try {
      const response = await client.messages.create({ model: 'fixture-flash', max_tokens: 4096, messages: [{ role: 'user', content: 'delayed' }] })
      observation.outcome = { text: response.content.filter(block => block.type === 'text').map(block => block.text).join('\n') }
    } catch (error) {
      observation.outcome = error instanceof Error ? { name: error.name, message: error.message } : { name: typeof error, message: String(error) }
    }
    return observation
  } finally {
    globalThis.setTimeout = realSetTimeout
    observation.elapsedMs = performance.now() - start
  }
}

if (import.meta.main) {
  process.stdout.write(`${JSON.stringify(await observeRecoveryTimeout(process.env.API_TIMEOUT_MS ?? '', process.env.ANTHROPIC_BASE_URL!))}\n`)
}
