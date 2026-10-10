import { createRequire } from 'node:module'
import { mkdir, writeFile, readFile, realpath } from 'node:fs/promises'
import { createHash } from 'node:crypto'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock } from 'bun:test'

const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private SSO fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
let current: any
let calls: any[] = []
// Only the external SDK transport is substituted. The installed fromSSO,
// token-provider, INI loader, SHA1 cache path, file write and error logic run.
mock.module('@aws-sdk/nested-clients/sso', () => ({
  SSOClient: class { constructor(options: any) { this.options = options } async send(command: any) { calls.push({ kind: 'role', region: this.options.region, input: command.input }); if (current.roleError) throw new Error(current.roleError); return current.roleResult || { roleCredentials: { accessKeyId: 'SSO_PRIVATE', secretAccessKey: 'SSO_SECRET', sessionToken: 'SSO_SESSION', expiration: 1900000000000 } } } },
  GetRoleCredentialsCommand: class { constructor(input: any) { this.input = input } }
}))
mock.module('@aws-sdk/nested-clients/sso-oidc', () => ({
  SSOOIDCClient: class { constructor(options: any) { this.options = options } async send(command: any) { calls.push({ kind: 'refresh', region: this.options.region, input: command.input }); if (current.refreshError) throw new Error(current.refreshError); return current.refreshResult || { accessToken: 'REFRESHED_PRIVATE', expiresIn: 3600, refreshToken: 'NEXT_REFRESH_PRIVATE' } } },
  CreateTokenCommand: class { constructor(input: any) { this.input = input } }
}))
const { fromSSO } = await import(join(dirname(require.resolve('@aws-sdk/credential-provider-sso')), '../dist-es/fromSSO.js'))
const input = await Bun.stdin.json()
const originalNow = Date.now
const rows: any[] = []
try {
  for (const row of input.rows) {
    current = row
    calls = []
    Date.now = () => row.now
    const dir = join(home, `sso-${rows.length}`)
    await mkdir(dir)
    const config = join(dir, 'config')
    const credentials = join(dir, 'credentials')
    await writeFile(config, row.config || '')
    await writeFile(credentials, row.credentials || '')
    const id = row.id || `https://private-${rows.length}.awsapps.com/start`
    const tokenPath = join(home, '.aws', 'sso', 'cache', createHash('sha1').update(id).digest('hex') + '.json')
    await mkdir(dirname(tokenPath), { recursive: true })
    if (row.token !== undefined) await writeFile(tokenPath, typeof row.token === 'string' ? row.token : JSON.stringify(row.token))
    Object.assign(process.env, { AWS_CONFIG_FILE: config, AWS_SHARED_CREDENTIALS_FILE: credentials, AWS_PROFILE: row.profile || 'selected' })
    let result: any
    try {
      const value = await fromSSO({ profile: row.profile || 'selected', configFilepath: config, filepath: credentials })()
      delete value.$source
      result = { value, calls }
    } catch (error) { result = { error: error.message, calls } }
    try { result.token = JSON.parse(await readFile(tokenPath, 'utf8')) } catch {}
    rows.push(result)
  }
} finally { Date.now = originalNow }
console.log(JSON.stringify({ rows }))
