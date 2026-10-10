import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native Slack leaves')
if (process.env.CC_HAHA_SLACK_LEAVES_FIXTURE !== '1') throw new Error('Unowned Slack fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { extractSlackPayload } = await import('../../../adapters/slack/extract-payload')
const { buildSlackManifestJson, buildSlackCreateAppUrl } = await import('../../../adapters/slack/manifest')
const { SlackApiClient, assertSlackFileUrl, assertSlackUploadUrl } = await import('../../../adapters/slack/api')
const calls: any[] = []
const responses = [...input.responses]
const api = new SlackApiClient('private-bot', async (url, options) => {
  calls.push({ url: String(url), method: options?.method ?? 'GET', headers: options?.headers ?? {}, body: typeof options?.body === 'string' ? JSON.parse(options.body) : options?.body ? [...options.body as Uint8Array] : null })
  const value = responses.shift()
  return new Response(value.raw ?? JSON.stringify(value.data), { status: value.status ?? 200 })
})
const results = []
for (const action of input.actions) {
  try {
    if (action.kind === 'post') results.push(await api.postMessage(action.channel, action.text, action.thread))
    if (action.kind === 'update') { await api.updateMessage(action.channel, action.ts, action.text); results.push(null) }
    if (action.kind === 'socket') results.push(await api.openSocketConnection('private-app'))
    if (action.kind === 'upload') { await api.uploadFile({ channel: action.channel, buffer: Buffer.from(action.bytes), filename: action.name, title: action.title, threadTs: action.thread }); results.push(null) }
    if (action.kind === 'download') results.push([...await api.downloadFile(action.url)])
    if (action.kind === 'auth') results.push(await api.authTest())
  } catch (err) { results.push({ error: (err as Error).message, status: (err as any).status ?? null }) }
}
process.stdout.write(JSON.stringify({ payloads: input.events.map((e: any) => extractSlackPayload(e, { botUserId: 'UBOT' })), urls: input.urls.map((u: any) => { try { return (u.upload ? assertSlackUploadUrl(u.value) : assertSlackFileUrl(u.value)).toString() } catch (err) { return { error: (err as Error).message } } }), manifest: buildSlackManifestJson(), createAppUrl: buildSlackCreateAppUrl(), calls, results }))
