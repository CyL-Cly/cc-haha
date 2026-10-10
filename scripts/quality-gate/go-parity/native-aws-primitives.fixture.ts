import { createRequire } from 'node:module'
import { mkdir, writeFile, realpath } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') {
  throw new Error('private AWS fixture root required')
}
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { SignatureV4 } = require('@smithy/signature-v4')
const { Sha256 } = require('@aws-crypto/sha256-js')
const { parseIni } = await import(join(dirname(require.resolve('@smithy/shared-ini-file-loader')), '../dist-es/parseIni.js'))
const { fromEnv } = require('@aws-sdk/credential-provider-env')
const { defaultProvider } = require('@aws-sdk/credential-provider-node')
const { EventStreamCodec } = require('@smithy/eventstream-codec')
const { getChunkedStream } = await import(join(dirname(require.resolve('@smithy/eventstream-serde-universal')), '../dist-es/getChunkedStream.js'))
const input = await Bun.stdin.json()
const rows = []
for (const row of input.rows) {
  try {
    if (row.kind === 'eventChunks') {
      const codec = new EventStreamCodec((bytes: any) => new TextDecoder().decode(bytes), (text: string) => new TextEncoder().encode(text))
      const messages = []
      const chunks = { async *[Symbol.asyncIterator]() { for (const hex of row.chunks) yield Buffer.from(hex, 'hex') } }
      let error = null
      try { for await (const message of getChunkedStream(chunks)) messages.push(codec.decode(message).body.toString()) }
      catch (failure) { error = failure.message }
      rows.push({ messages, error })
    } else if (row.kind === 'event') {
      const codec = new EventStreamCodec((bytes: any) => new TextDecoder().decode(bytes), (text: string) => new TextEncoder().encode(text))
      const value = codec.decode(Buffer.from(row.hex, 'hex'))
      rows.push({ value: { headers: value.headers, body: new TextDecoder().decode(value.body) } })
    } else if (row.kind === 'sign') {
      const signer = new SignatureV4({ region: row.region, service: row.service, credentials: row.credentials, sha256: Sha256, applyChecksum: !row.disableChecksum, uriEscapePath: !row.disablePathEscape })
      rows.push({ value: await signer.sign({ method: row.method, path: row.path, query: row.query, headers: row.headers, body: row.body }, { signingDate: new Date(row.now) }) })
    } else if (row.kind === 'ini') {
      rows.push({ value: parseIni(row.data) })
    } else {
      for (const key of Object.keys(process.env)) if (key.startsWith('AWS_')) delete process.env[key]
      const aws = join(home, `aws-${rows.length}`)
      await mkdir(aws)
      const config = join(aws, 'config')
      const credentials = join(aws, 'credentials')
      await writeFile(config, row.config || '')
      await writeFile(credentials, row.credentialsFile || '')
      Object.assign(process.env, { AWS_EC2_METADATA_DISABLED: 'true', AWS_CONFIG_FILE: config, AWS_SHARED_CREDENTIALS_FILE: credentials }, row.env)
      const calls = []
      const roleOptions = row.roleResult ? {
        roleAssumer: async (credentials: any, params: any) => {
          delete credentials.$source
          calls.push({ credentials, params })
          return { ...row.roleResult }
        },
        roleAssumerWithWebIdentity: async (params: any) => {
          calls.push({ params })
          return { ...row.roleResult }
        }
      } : {}
      const value = await (row.kind === 'env' ? fromEnv()() : defaultProvider(roleOptions)())
      delete value.$source
      rows.push({ value, ...(row.roleResult ? { calls } : {}) })
    }
  } catch (error) {
    rows.push({ error: error.message })
  }
}
console.log(JSON.stringify({ rows }))
