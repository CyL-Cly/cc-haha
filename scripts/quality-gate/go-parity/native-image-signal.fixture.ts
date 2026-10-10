import { assertDisposableFixtureHome } from './fixture-guard'
import { spyOn } from 'bun:test'
assertDisposableFixtureHome(process.env.HOME, 'Image signal lifecycle')
if (process.env.CC_HAHA_IMAGE_SIGNAL_FIXTURE !== '1') throw new Error('Unowned image signal fixture')
const rows = await Bun.stdin.json()
const physicalTimeout = globalThis.setTimeout
let current: any
const timer = spyOn(globalThis, 'setTimeout').mockImplementation(((callback: any, delay: any, ...args: any[]) => {
  if (delay === 600000) {
    current.armed = delay
    return physicalTimeout(callback, 25, ...args)
  }
  return physicalTimeout(callback, delay, ...args)
}) as typeof setTimeout)
const { generateImages } = await import('../../../src/tools/ImageGenTool/backend')
const output = []
try {
  for (const row of rows) {
    current = {}
    const parent = new AbortController()
    if (row.kind === 'preaborted') parent.abort(new Error('private custom reason'))
    if (row.kind === 'caller_abort') physicalTimeout(() => parent.abort(new Error('private custom reason')), 25)
    let captured: AbortSignal | undefined
    let error: string | undefined
    let images = 0
    try {
      const value = await generateImages({ prompt: 'original combined image signal', count: 1 }, { kind: 'openai_images', providerId: 'private', model: 'private-image', apiKey: 'private-fake-key', baseUrl: 'https://private.invalid' }, {
        signal: parent.signal, outputDir: `${process.env.HOME}/${row.kind}`,
        async fetchImpl(_url, init) {
          captured = init?.signal ?? undefined
          return fetch(row.url, init)
        },
      })
      images = value.images.length
    } catch (failure) { error = (failure as Error).message }
    await new Promise(resolve => physicalTimeout(resolve, 40))
    output.push({ kind: row.kind, armed: current.armed ?? null, aborted: captured?.aborted ?? false, images, ...(error ? { error } : {}) })
  }
  process.stdout.write(JSON.stringify(output))
} finally { timer.mockRestore() }
