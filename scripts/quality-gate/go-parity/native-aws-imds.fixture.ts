import { createRequire } from 'node:module'
import { mkdir, realpath, writeFile } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock } from 'bun:test'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private IMDS fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const root = dirname(require.resolve('@smithy/credential-provider-imds'))
let current: any
let calls: any[]
let cursor = 0
mock.module(join(root, '../dist-es/remoteProvider/httpRequest.js'), () => ({ httpRequest: async (options) => {
  calls.push({ method: options.method || 'GET', hostname: options.hostname, path: options.path, headers: options.headers || {}, timeout: options.timeout })
  const step = current.responses[cursor++]
  if (!step) throw new Error('unconfigured metadata primitive')
  if (step.error) throw Object.assign(new Error(step.error), step.status ? { statusCode: step.status } : {})
  return Buffer.from(typeof step.body === 'string' ? step.body : JSON.stringify(step.body))
} }))
const { fromInstanceMetadata } = await import(join(root, '../dist-es/fromInstanceMetadata.js'))
const input = await Bun.stdin.json()
const rows = []
const now = Date.now
const random = Math.random
try {
  for (const row of input.rows) {
    current = row
    cursor = 0
    calls = []
    Date.now = () => row.now
    Math.random = () => row.random ?? 0.25
    const dir = join(home, `imds-${rows.length}`)
    await mkdir(dir)
    const config = join(dir, 'config')
    const credentials = join(dir, 'credentials')
    await writeFile(config, row.config || '')
    await writeFile(credentials, '')
    for (const key of Object.keys(process.env)) if (key.startsWith('AWS_')) delete process.env[key]
    Object.assign(process.env, { AWS_CONFIG_FILE: config, AWS_SHARED_CREDENTIALS_FILE: credentials, AWS_EC2_METADATA_SERVICE_ENDPOINT: 'http://127.0.0.1:1' }, row.env)
    const provider = fromInstanceMetadata({ logger: { warn() {}, debug() {} } })
    const values = []
    try {
      for (let n = 0; n < (row.count ?? 1); n++) values.push(await provider())
      rows.push({ value: values, calls })
    } catch (error) { rows.push({ error: error.message, calls }) }
  }
} finally { Date.now = now; Math.random = random }
console.log(JSON.stringify({ rows }))
