import { assertDisposableFixtureHome } from './fixture-guard'
import { mock } from 'bun:test'
import * as actual from 'node:fs/promises'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Image input Promise.all')
if (process.env.CC_HAHA_IMAGE_INPUT_PARALLEL_FIXTURE !== '1') throw new Error('Unowned image input fixture')
const roots = [0, 1, 2].map(index => join(process.env.HOME!, `root${index}`))
await Promise.all(roots.map(root => actual.mkdir(root)))
const bytes = Buffer.from('89504e470d0a1a0a00000000', 'hex')
const paths = [0, 1, 2].map(index => join(roots[0]!, `image${index}.png`))
await Promise.all(paths.map(path => actual.writeFile(path, bytes)))
const outside = join(process.env.HOME!, 'not-provided.png')
await actual.writeFile(outside, bytes)
const physical = { ...actual }
let current: any
mock.module('fs/promises', () => ({ ...physical,
  realpath(path: any) {
    const value = String(path)
    if (roots.includes(value)) {
      return new Promise((resolve, reject) => {
        current.rootWaiters.push({ value, resolve, reject })
        if (current.rootWaiters.length === 3) {
          current.allRootsStarted = true
          for (const item of [...current.rootWaiters].reverse()) physical.realpath(item.value).then(item.resolve, item.reject)
        }
      })
    }
    if (current.requested.includes(value)) {
      return new Promise((resolve, reject) => {
        current.imageWaiters.push({ value, resolve, reject })
        if (current.imageWaiters.length === 3) {
          current.allImagesStarted = true
          for (const item of [...current.imageWaiters].reverse()) {
            if (current.reject && item.value === paths[0]) current.releaseFirst = () => physical.realpath(item.value).then(item.resolve, item.reject)
            else physical.realpath(item.value).then(item.resolve, item.reject)
          }
        }
      })
    }
    return physical.realpath(path)
  },
  async open(path: any, ...args: any[]) {
    const file = await (physical.open as any)(path, ...args)
    if (typeof args[0] === 'number') {
      current.reads++
      if (current.reads === current.expectedReads) current.readsDone()
    }
    return file
  },
}))
const { generateImages } = await import('../../../src/tools/ImageGenTool/backend')
const output = []
for (const reject of [false, true]) {
  let readsDone: () => void = () => {}
  const allReads = new Promise<void>(resolve => { readsDone = resolve })
  current = { reject, requested: reject ? [paths[0], outside, paths[2]] : paths, rootWaiters: [], imageWaiters: [], reads: 0, expectedReads: reject ? 2 : 3, readsDone }
  const names: string[] = []
  let error: string | undefined
  try {
    await generateImages({ prompt: 'parallel original images', count: 1, referenced_image_paths: current.requested }, { kind: 'openai_images', providerId: 'private', model: 'private-image', apiKey: 'private-fake-key', baseUrl: 'https://private.invalid' }, {
      inputRootDirs: roots, outputDir: join(process.env.HOME!, reject ? 'rejected-output' : 'success-output'),
      async fetchImpl(_url, init) {
        for (const [, value] of (init!.body as FormData).entries()) if (value instanceof File) names.push(value.name)
        return new Response(JSON.stringify({ data: [{ b64_json: bytes.toString('base64') }] }))
      },
    })
  } catch (failure) { error = (failure as Error).message }
  const rejectedBeforeRelease = Boolean(error && current.releaseFirst)
  current.releaseFirst?.()
  await allReads
  await new Promise(resolve => setTimeout(resolve, 15))
  output.push({ reject, allRootsStarted: current.allRootsStarted, allImagesStarted: current.allImagesStarted, rejectedBeforeRelease, reads: current.reads, names, ...(error ? { error: error.replace(process.env.HOME!, '$HOME') } : {}) })
}
process.stdout.write(JSON.stringify(output))
