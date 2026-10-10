import { assertDisposableFixtureHome } from './fixture-guard'
import { mock, spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
assertDisposableFixtureHome(process.env.HOME, 'Image generation service')
if (process.env.CC_HAHA_IMAGEGEN_SERVICE_FIXTURE !== '1') throw new Error('Unowned image service fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
let current: any
let refreshes = 0
mock.module('../../../src/services/openaiAuth/index', () => ({ ensureFreshOpenAITokens: async () => current.tokens ?? null }))
mock.module('../../../src/services/grokAuth/refresh', () => ({ ensureFreshGrokTokens: async () => current.tokens ?? null, forceRefreshGrokTokens: async () => { refreshes++; if (current.refreshError) throw new Error('private refresh failed'); return current.refreshed ?? null } }))
const originalNow = Date.now
Date.now = () => 1770000000000
const uuid = spyOn(crypto, 'randomUUID').mockImplementation(() => 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa')
const { generateImages } = await import('../../../src/tools/ImageGenTool/backend')
const output = []
try {
  for (const [index, row] of rows.entries()) {
    current = row
    refreshes = 0
    const root = join(process.env.HOME!, `row${index}`)
    const inputs = join(root, 'inputs')
    const dir = join(root, 'output')
    await mkdir(inputs, { recursive: true })
    const input = { ...row.input }
    if (row.edit) {
      const source = join(inputs, 'source.png')
      await writeFile(source, Buffer.from(row.inputBytes, 'base64'), { mode: 0o600 })
      input.referenced_image_paths = [source]
    }
    const requests: any[] = []
    try {
      const value = await generateImages(input, row.config, {
        inputRootDirs: [inputs], outputDir: dir,
        async fetchImpl(url, init) {
          let body: any = init?.body
          if (body instanceof FormData) {
            body = await Promise.all([...body.entries()].map(async ([name, value]) => [name, typeof value === 'string' ? value : { name: (value as File).name, type: value.type, bytes: Buffer.from(await value.arrayBuffer()).toString('base64') }]))
          } else if (typeof body === 'string') body = JSON.parse(body)
          requests.push({ url: String(url), method: init?.method, headers: [...new Headers(init?.headers)], body })
          const response = row.responses[Math.min(requests.length - 1, row.responses.length - 1)]
          return new Response(response.body, { status: response.status, headers: response.headers ?? {} })
        },
        ...(row.download ? { downloadImage: async () => Buffer.from(row.download, 'base64') } : {}),
      })
      output.push({ value: { ...value, images: value.images.map(image => ({ ...image, path: image.path.replace(root, '$ROOT') })) }, requests, refreshes, files: await Promise.all(value.images.map(async image => ({ path: image.path.replace(root, '$ROOT'), bytes: (await readFile(image.path)).toString('base64'), mode: (await stat(image.path)).mode & 0o777 }))) })
    } catch (error) {
      const files = await readdir(dir).catch(() => [])
      output.push({ error: (error as Error).message, requests, refreshes, files })
    }
  }
  process.stdout.write(JSON.stringify(output))
} finally { Date.now = originalNow; uuid.mockRestore() }
