import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, readFileSync, symlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'Native Telegram media')
if (process.env.CC_HAHA_TELEGRAM_MEDIA_FIXTURE !== '1') throw new Error('Unowned Telegram media fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { Bot } = await import(Bun.resolveSync('grammy', new URL('../../../adapters/telegram/', import.meta.url).pathname))
const { TelegramMediaService } = await import('../../../adapters/telegram/media')
const { AttachmentStore } = await import('../../../adapters/common/attachment/attachment-store')
const { checkAttachmentLimit } = await import('../../../adapters/common/attachment/attachment-limits')
const { ImageBlockWatcher } = await import('../../../adapters/common/attachment/image-block-watcher')
const { loadSafeOutboundImage } = await import('../../../adapters/common/attachment/outbound-image')
const calls: unknown[] = []
const host = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const url = new URL(request.url)
  if (url.pathname.endsWith('/sendPhoto')) {
    const form = await request.formData()
    const reference = String(form.get('photo')).replace('attach://', '')
    const file = form.get(reference) as File
    calls.push({ method: 'sendPhoto', chat: form.get('chat_id'), caption: form.get('caption'), file: { name: file.name, mime: file.type, data: Buffer.from(await file.arrayBuffer()).toString('base64') } })
    return Response.json({ ok: true, result: true })
  }
  if (url.pathname.endsWith('/getFile')) {
    const payload = await request.json()
    calls.push({ method: 'getFile', payload })
    return Response.json({ ok: true, result: payload.file_id === 'missing' ? {} : { file_path: `media/${payload.file_id}` } })
  }
  calls.push({ method: 'download', path: url.pathname })
  if (url.pathname.endsWith('/failure')) return new Response('', { status: 503 })
  return new Response(Buffer.from(input.data, 'base64'), { headers: { 'content-type': 'application/octet-stream' } })
} })
const originalFetch = globalThis.fetch
globalThis.fetch = ((url: any, options: any) => {
  if (String(url).startsWith('https://api.telegram.org/file/botprivate-token/')) return originalFetch(`http://127.0.0.1:${host.port}${new URL(url).pathname}`, options)
  return originalFetch(url, options)
}) as typeof fetch
const store = new AttachmentStore({ root: join(home, 'store') })
const bot = new Bot('private-token', { client: { apiRoot: `http://127.0.0.1:${host.port}` } })
const media = new TelegramMediaService(bot, store)
const downloads = []
try {
  for (const profile of input.downloads) {
    try {
      const result = await media.downloadFile(profile.id, profile.session, profile.hint)
      downloads.push({ ...result, buffer: result.buffer.toString('base64'), path: result.path.slice(join(home, 'store').length), stored: readFileSync(result.path).toString('base64'), check: checkAttachmentLimit(result.kind, result.size, result.mimeType) })
    } catch (err) { downloads.push({ error: err instanceof Error ? err.message : String(err) }) }
  }
  const watcher = new ImageBlockWatcher()
  const images = input.chunks.map((chunk: string) => watcher.feed(chunk))
  const work = join(home, 'work')
  mkdirSync(work, { recursive: true })
  writeFileSync(join(work, 'inside.png'), Buffer.from(input.data, 'base64'))
  const outside = join(home, 'outside.png')
  writeFileSync(outside, Buffer.from(input.data, 'base64'))
  symlinkSync(outside, join(work, 'escape.png'))
  const safe = []
  for (const profile of input.outbound) {
    const source = { ...profile.source }
    if (source.path) source.path = source.path.replace('$work', work).replace('$home', home)
    const result = await loadSafeOutboundImage({ id: 'private', source }, profile.noWork ? null : work)
    safe.push(result.ok ? { ...result, buffer: result.buffer.toString('base64') } : result)
  }
  await media.sendPhoto(42, Buffer.from(input.data, 'base64'), 'private 😀 caption')
  process.stdout.write(JSON.stringify({ downloads, calls, images, drain: watcher.drain(), safe, limits: input.limits.map((p: any) => checkAttachmentLimit(p.kind, p.size, p.mime)) }))
} finally { globalThis.fetch = originalFetch; host.stop(true) }
