import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native Telegram API polling')
if (process.env.CC_HAHA_TELEGRAM_API_POLL_FIXTURE !== '1') throw new Error('Unowned Telegram API fixture')
const { Bot } = await import(Bun.resolveSync('grammy', new URL('../../../adapters/telegram/', import.meta.url).pathname))
const { syncTelegramBotCommands } = await import('../../../adapters/telegram/menu')
const calls: Array<{ method: string; payload: any }> = []
let resolveBlocked!: () => void
const blocked = new Promise<void>(resolve => { resolveBlocked = resolve })
let pollCount = 0
let meCount = 0
let webhookCount = 0
let blockedAborted = false
const peer = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  const method = new URL(request.url).pathname.split('/').pop()!
  const payload = await request.json()
  calls.push({ method, payload })
  if (method === 'getMe') {
    if (++meCount === 1) return Response.json({ ok: false, error_code: 500, description: 'private setup retry' })
    return Response.json({ ok: true, result: { id: 999, is_bot: true, username: 'private_test_bot', first_name: 'Private' } })
  }
  if (method === 'deleteWebhook' && ++webhookCount === 1) return Response.json({ ok: false, error_code: 429, description: 'private setup throttle', parameters: { retry_after: 0 } })
  if (method === 'getUpdates') {
    if (payload.limit === 1) return Response.json({ ok: true, result: [] })
    pollCount++
    if (pollCount === 1) return Response.json({ ok: false, error_code: 429, description: 'private poll throttle', parameters: { retry_after: 0 } })
    if (pollCount === 2) return Response.json({ ok: true, result: [{ update_id: 7, message: { message_id: 10, date: 1, from: { id: 42, is_bot: false, first_name: 'Fixture' }, chat: { id: 42, type: 'private' }, text: 'first' } }, { update_id: 9, message: { message_id: 11, date: 1, from: { id: 42, is_bot: false, first_name: 'Fixture' }, chat: { id: 42, type: 'private' }, text: 'second' } }] })
    resolveBlocked()
    await new Promise<void>(resolve => request.signal.addEventListener('abort', () => { blockedAborted = true; resolve() }, { once: true }))
    return Response.json({ ok: true, result: [] })
  }
  if (method === 'sendMessage') return Response.json({ ok: true, result: { message_id: 100 } })
  return Response.json({ ok: true, result: true })
} })
const bot = new Bot('private-token-never-sent', { client: { apiRoot: `http://127.0.0.1:${peer.port}` } })
bot.on('message:text', ctx => ctx.reply(`received ${ctx.message.text}`))
try {
  await syncTelegramBotCommands(bot.api)
  const running = bot.start()
  await blocked
  await bot.stop()
  await running
  for (let i = 0; !blockedAborted && i < 100; i++) await new Promise(resolve => setTimeout(resolve, 1))
  const initial = calls.filter(call => call.method === 'getMe' || call.method === 'deleteWebhook').sort((a, b) => a.method.localeCompare(b.method))
  const rest = calls.filter(call => call.method !== 'getMe' && call.method !== 'deleteWebhook')
  process.stdout.write(JSON.stringify({ initial, rest, blockedAborted }))
} finally { peer.stop(true) }
