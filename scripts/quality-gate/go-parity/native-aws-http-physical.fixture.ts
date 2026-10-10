import { createRequire } from 'node:module'
import { realpath, writeFile } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private HTTP credential fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { fromHttp } = require('@aws-sdk/credential-provider-http')
const input = await Bun.stdin.json()
const target = new URL(input.url)
const proxy = new URL(input.proxy)
if (target.hostname !== '127.0.0.1' || proxy.hostname !== '127.0.0.1') throw new Error('loopback only')
const tokenPath = join(home, 'token-private')
await writeFile(tokenPath, 'TOKEN_FILE_PRIVATE')
Object.assign(process.env, { AWS_CONTAINER_CREDENTIALS_FULL_URI: target.href, AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: tokenPath, HTTP_PROXY: proxy.href, HTTPS_PROXY: proxy.href, http_proxy: proxy.href, https_proxy: proxy.href, NO_PROXY: '', no_proxy: '' })
try {
  const value = await fromHttp({ timeout: 1000, maxRetries: 0 })()
  delete value.$source
  console.log(JSON.stringify({ value }))
} catch (error) { console.log(JSON.stringify({ error: error.message })) }
