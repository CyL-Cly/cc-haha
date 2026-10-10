import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Native Telegram delivery')
if (process.env.CC_HAHA_TELEGRAM_DELIVERY_FIXTURE !== '1') throw new Error('Unowned Telegram delivery fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const format = await import('../../../adapters/telegram/format')
const common = await import('../../../adapters/common/format')
const permissions = await import('../../../adapters/common/permission')
const { TelegramStreamDelivery } = await import('../../../adapters/telegram/stream-delivery')
const rows = []
for (const profile of input.profiles) {
  if (profile.kind === 'format') {
    rows.push({ outbound: format.formatTelegramOutboundText(profile.text), streaming: format.formatTelegramStreamingText(profile.text), split: common.splitMessage(profile.text, profile.limit), chunk: format.splitTelegramStreamingChunk(profile.text, profile.limit), thinking: format.buildTelegramThinkingUpdate(profile.current, profile.text, profile.preview), plan: format.planTelegramStreamingUpdate(profile.current, profile.text, profile.limit) })
  } else if (profile.kind === 'permission') {
    rows.push({ command: permissions.parsePermissionCommand(profile.text, profile.pending), callback: permissions.parsePermitCallbackData(profile.callback), instruction: permissions.formatPermissionInstructions(profile.id), status: permissions.formatPermissionDecisionStatus(profile.decision) })
  } else {
    const calls: unknown[] = []
    const sleeps: number[] = []
    const snapshots: unknown[] = []
    let nextId = 1
    const failures = [...(profile.failures ?? [])]
    const api = {
      async sendMessage(chat: number, text: string) {
        calls.push({ method: 'send', chat, text })
        const failure = failures[0]
        if (failure?.method === 'send') { failures.shift(); throw failure.error }
        return { message_id: nextId++ }
      },
      async editMessageText(chat: number, id: number, text: string) {
        calls.push({ method: 'edit', chat, id, text })
        const failure = failures[0]
        if (failure?.method === 'edit') { failures.shift(); throw failure.error }
      },
    }
    const delivery = new TelegramStreamDelivery(api, { bufferIntervalMs: 60_000, bufferCharThreshold: profile.threshold ?? 200, sleep: async ms => { sleeps.push(ms) }, warn: () => {} })
    for (const event of profile.events) {
      let error: unknown = undefined
      try { await delivery.handleEvent('42', event) } catch (err) { error = err }
      await new Promise(resolve => setTimeout(resolve, 0))
      snapshots.push({ state: delivery.getDeliveryState('42') ?? null, error })
    }
    delivery.clear('42')
    rows.push({ calls, sleeps, snapshots })
  }
}
process.stdout.write(JSON.stringify(rows))
