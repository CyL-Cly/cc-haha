import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native fetch redirect')
if (process.env.CC_HAHA_FETCH_REDIRECT_FIXTURE !== '1') throw new Error('Unowned fetch redirect fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const output = []
for (const row of rows) {
  const url = new URL(row.url)
  if (!['127.0.0.1', 'localhost'].includes(url.hostname)) throw new Error('Non-loopback fetch fixture')
  try {
    const response = await fetch(url, { method: row.method, headers: { ...row.headers, 'X-Fixture-Run': 'source' }, ...(row.body ? { body: row.body } : {}), redirect: row.mode })
    output.push({ status: response.status, url: response.url, location: response.headers.get('location'), duplicate: response.headers.get('x-duplicate'), text: await response.text() })
  } catch (error) {
    output.push({ error: (error as Error).message, code: (error as any).code ?? null })
  }
}
process.stdout.write(JSON.stringify(output))
