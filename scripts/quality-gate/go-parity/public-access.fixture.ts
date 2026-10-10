import { assertDisposableFixtureHome } from './fixture-guard'
import { join } from 'node:path'

const home = assertDisposableFixtureHome(process.env.HOME, 'Public access')
if (process.env.CC_HAHA_PUBLIC_ACCESS_FIXTURE !== '1') throw new Error('Unowned public access fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { PublicAccessServer, migratePublicAccessStore, isPublicBusinessPathAllowed } = await import('../../../src/server/publicAccess')
const result: any = { migrations: [], paths: [], controls: [], lifecycle: [] }
for (const value of input.migrations) {
  try { result.migrations.push({ value: migratePublicAccessStore(value) }) }
  catch (error) { result.migrations.push({ error: (error as Error).message }) }
}
for (const row of input.paths) result.paths.push(isPublicBusinessPathAllowed(new URL(row.path, 'https://fixture.invalid'), row.method))
let now = 1000
const service = new PublicAccessServer({ storePath: join(home, 'devices.json'), now: () => now, serverPort: () => 3456,
  handleApiRequest: async () => Response.json({ hasAuth: true, source: 'env', env: { PRIVATE_KEY: 'fake-secret' }, language: 'en', autoQuestion: { enabled: true } }),
  handleStatic: async () => new Response('fixture-static'), websocket: { message() {} },
})
try {
  for (const row of input.controls) {
    process.env.CC_HAHA_LOCAL_ACCESS_TOKEN = row.configured ?? 'fixture-process-credential'
    if (row.advance) now += row.advance
    const response = await service.control(new Request(`http://localhost${row.path}`, { method: row.method, headers: row.headers, ...(row.method === 'GET' ? {} : { body: row.body ?? '{}' }) }))
    const body = await response.json() as any
    if (body.port) body.port = '<actual-port>'
    if (body.secret) body.secret = '<actual-secret>'
    result.controls.push({ status: response.status, body, headers: Object.fromEntries(response.headers) })
  }
  process.env.CC_HAHA_LOCAL_ACCESS_TOKEN = 'fixture-process-credential'
  const values: Record<string, string> = {}
  for (const row of input.lifecycle ?? []) {
    if (row.advance) now += row.advance
    const body = JSON.stringify(row.body ?? {}).replace(/\$([a-zA-Z]+)/g, (_, key) => values[key] ?? '')
    let response: Response
    if (row.local) {
      response = await service.control(new Request(`http://localhost${row.path}`, { method: row.method, headers: { Authorization: 'Bearer fixture-process-credential' }, ...(row.method === 'GET' ? {} : { body }) }))
    } else {
      response = await fetch(`http://127.0.0.1:${service.status().port}${row.path}`, { method: row.method,
        headers: { ...(row.origin !== null ? { Origin: row.origin ?? 'https://fixture.invalid' } : {}), ...(row.cookie ? { Cookie: values.cookie! } : {}), ...(row.headers ?? {}) },
        ...(row.method === 'GET' || row.method === 'HEAD' ? {} : { body }), redirect: 'manual',
      })
    }
    const cookie = response.headers.get('set-cookie')
    if (cookie) values.cookie = cookie.split(';')[0]!
    const value = await response.json() as any
    if (value.secret) { values.secret = value.secret; value.secret = '<actual-secret>' }
    if (value.claimSecret) { values.id = value.id; values.claimSecret = value.claimSecret; value.id = '<actual-id>'; value.claimSecret = '<actual-claim>' }
    if (value.port) value.port = '<actual-port>'
    for (const key of ['pending', 'devices']) for (const device of value[key] ?? []) device.id = '<actual-id>'
    const headers = Object.fromEntries(response.headers)
    delete headers.date; delete headers['content-length']; delete headers['transfer-encoding']; delete headers.connection; delete headers['keep-alive']
    if (headers['set-cookie']) headers['set-cookie'] = headers['set-cookie'].replace(/^([^=]+=)[^;]+/, '$1<actual-cookie>')
    result.lifecycle.push({ status: response.status, body: value, headers })
  }
} finally { service.disable() }
process.stdout.write(JSON.stringify(result))
