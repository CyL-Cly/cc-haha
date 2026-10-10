import { assertDisposableFixtureHome } from './fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Native fetch compression')
if (process.env.CC_HAHA_FETCH_COMPRESSION_FIXTURE !== '1') throw new Error('Unowned fetch compression fixture')
const rows = JSON.parse(await Bun.stdin.text())
const output = []
for (const row of rows) {
  const url = new URL(row.url)
  if (!['127.0.0.1', 'localhost'].includes(url.hostname)) throw new Error('Non-loopback compression fixture')
  const response = await fetch(url, { headers: { 'X-Fixture-Run': 'source', ...row.headers }, ...(row.ca ? { tls: { ca: row.ca } } : {}) })
  output.push({ status: response.status, encoding: response.headers.get('content-encoding'), length: response.headers.get('content-length'), body: Buffer.from(await response.arrayBuffer()).toString('base64') })
}
process.stdout.write(JSON.stringify(output))
