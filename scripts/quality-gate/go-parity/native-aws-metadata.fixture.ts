import { createRequire } from 'node:module'
import { realpath } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { Readable } from 'node:stream'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private metadata fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const root = dirname(require.resolve('@aws-sdk/credential-provider-http'))
const { checkUrl } = await import(join(root, '../dist-es/fromHttp/checkUrl.js'))
const { createGetRequest, getCredentials } = await import(join(root, '../dist-es/fromHttp/requestHelpers.js'))
const { isImdsCredentials, fromImdsCredentials } = await import(join(dirname(require.resolve('@smithy/credential-provider-imds')), '../dist-es/remoteProvider/ImdsCredentials.js'))
const input = await Bun.stdin.json()
const rows = []
for (const row of input.rows) {
  try {
    if (row.kind === 'url') {
      const url = new URL(row.url)
      checkUrl(url)
      const request = createGetRequest(url)
      rows.push({ value: { method: request.method, path: request.path, query: request.query, hostname: request.hostname, protocol: request.protocol, port: request.port || null } })
    } else if (row.kind === 'legacy') {
      if (!isImdsCredentials(row.body)) throw new Error('Invalid response received from instance metadata service.')
      rows.push({ value: fromImdsCredentials(row.body) })
    } else {
      rows.push({ value: await getCredentials({ statusCode: row.status ?? 200, body: Readable.from([Buffer.from(JSON.stringify(row.body))]) }) })
    }
  } catch (error) { rows.push({ error: error.message }) }
}
console.log(JSON.stringify({ rows }))
