import { createRequire } from 'node:module'
import { realpath } from 'node:fs/promises'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
const home = process.env.HOME
if (!home || await realpath(process.cwd()) !== await realpath(home) || process.env.CC_HAHA_AWS_FIXTURE !== '1') throw new Error('private SSO wire fixture root required')
const require = createRequire(join(dirname(fileURLToPath(import.meta.url)), '../../../package.json'))
const { SSOClient, GetRoleCredentialsCommand } = require('@aws-sdk/nested-clients/sso')
const { SSOOIDCClient, CreateTokenCommand } = require('@aws-sdk/nested-clients/sso-oidc')
const input = await Bun.stdin.json()
const rows = []
for (const row of input.rows) {
  let captured
  const requestHandler = { async handle(request) {
    const body = request.body ? JSON.parse(request.body) : null
    captured = { method: request.method, path: request.path, query: request.query, body, headers: { ...(request.headers['content-type'] ? { 'content-type': request.headers['content-type'] } : {}), ...(request.headers['x-amz-sso_bearer_token'] ? { 'x-amz-sso_bearer_token': request.headers['x-amz-sso_bearer_token'] } : {}) } }
    const result = row.kind === 'role' ? { roleCredentials: { accessKeyId: 'A', secretAccessKey: 'B', sessionToken: 'C', expiration: 1900000000000 } } : { accessToken: 'D', expiresIn: 3600, refreshToken: 'E' }
    return { response: { statusCode: 200, headers: { 'content-type': 'application/json' }, body: new TextEncoder().encode(JSON.stringify(result)) } }
  } }
  const options = { region: row.region, endpoint: 'http://127.0.0.1:1', maxAttempts: 1, requestHandler }
  if (row.kind === 'role') await new SSOClient(options).send(new GetRoleCredentialsCommand(row.input))
  else await new SSOOIDCClient(options).send(new CreateTokenCommand(row.input))
  rows.push({ value: captured })
}
console.log(JSON.stringify({ rows }))
