import { assertDisposableFixtureHome } from './fixture-guard'
import { spyOn } from 'bun:test'
import { readdir } from 'node:fs/promises'
assertDisposableFixtureHome(process.env.HOME, 'Image late caller cancellation phase')
if (process.env.CC_HAHA_IMAGE_SIGNAL_FIXTURE !== '1' || Bun.version !== '1.3.14') throw new Error('Unowned image signal fixture')
const input = await Bun.stdin.json()
const physicalTimeout = globalThis.setTimeout
const physicalClear = globalThis.clearTimeout
let current: any
const mark = (event: string, data: any = {}) => current.phase.push({ event, ms: performance.now() - current.started, ...data })
const timer = spyOn(globalThis, 'setTimeout').mockImplementation(((callback: any, delay: any, ...args: any[]) => {
  if (delay === 600000) mark('timeout-arm', { originalDelay: delay, physicalDelay: delay })
  return physicalTimeout(callback, delay, ...args)
}) as typeof setTimeout)
const clear = spyOn(globalThis, 'clearTimeout').mockImplementation(((value: any) => { mark('timeout-clear'); return physicalClear(value) }) as typeof clearTimeout)
const { generateImages } = await import('../../../src/tools/ImageGenTool/backend')
const output = []
try {
  for (const kind of ['normal', 'abort-after-complete-provider-json']) {
    current = { phase: [], started: performance.now() }
    const parent = new AbortController()
    let captured: AbortSignal | undefined
    let error: string | undefined
    let images = 0
    const directory = `${process.env.HOME}/${kind}`
    try {
      const result = await generateImages({ prompt: 'original combined image signal', count: 1 }, { kind: 'openai_images', providerId: 'private', model: 'private-image', apiKey: 'private-fake-key', baseUrl: 'https://private.invalid' }, {
        signal: parent.signal, outputDir: directory,
        async fetchImpl(_url, init) {
          captured = init?.signal ?? undefined
          captured?.addEventListener('abort', () => mark('combined-abort', { aborted: captured?.aborted }), { once: true })
          mark('fetch-begin')
          const response = await fetch(input.url + '?kind=' + kind, init)
          mark('fetch-headers', { status: response.status })
          const originalJSON = response.json
          response.json = async () => {
            mark('provider-json-begin')
            const value = await Reflect.apply(originalJSON, response, [])
            mark('provider-json-complete', { aborted: captured?.aborted })
            if (kind === 'abort-after-complete-provider-json') {
              mark('caller-abort')
              parent.abort(new Error('private late caller cancellation'))
              mark('caller-abort-return', { aborted: captured?.aborted })
            }
            return value
          }
          return response
        },
      })
      images = result.images.length
      mark('generate-return', { images, aborted: captured?.aborted })
    } catch (failure) {
      error = (failure as Error).message
      mark('generate-error', { error, aborted: captured?.aborted })
    }
    const files = await readdir(directory).catch(() => [])
    output.push({ kind, images, aborted: captured?.aborted, files: files.length, ...(error ? { error } : {}), phase: current.phase })
  }
  process.stdout.write(JSON.stringify(output))
} finally { timer.mockRestore(); clear.mockRestore() }
