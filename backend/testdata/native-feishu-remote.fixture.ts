import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { isPublicRemoteAddress, loadSafeRemoteImage, materializePendingUploadImage } from '../../adapters/common/attachment/safe-remote-image'
assertDisposableFixtureHome(process.env.HOME, 'Feishu remote media Source')
if (process.env.CC_HAHA_FEISHU_REMOTE_FIXTURE !== '1') throw new Error('Unowned Feishu remote media fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const receipts: any[] = []
const results = []
for (const row of input.rows) {
  if (row.kind === 'address') { results.push(isPublicRemoteAddress(row.address)); continue }
  if (row.kind === 'materialize') {
    try { const local = await materializePendingUploadImage(row.source); results.push({ bytes: local.buffer.toString('base64'), mime: local.mime }) } catch (error: any) { results.push({ error: error.message }) }
    continue
  }
  const result = await loadSafeRemoteImage(row.url, {
    resolveHostname: async hostname => { receipts.push({ resolve: hostname }); return row.addresses },
    requestHop: async (url, address, timeoutMs) => {
      receipts.push({ url: url.href, address })
      const response = await fetch(input.peer + url.pathname + url.search, { redirect: 'manual' })
      return { status: response.status, headers: Object.fromEntries(response.headers), body: (async function* () { yield new Uint8Array(await response.arrayBuffer()) })(), destroy: () => {} }
    },
  })
  results.push(result.ok ? { ok: true, bytes: result.buffer.toString('base64'), mime: result.mime } : result)
}
process.stdout.write(JSON.stringify({ results, receipts }))
