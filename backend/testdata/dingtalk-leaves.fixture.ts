import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
if (process.env.CC_HAHA_DINGTALK_PRIVATE_FIXTURE !== '1') throw new Error('private fixture only')
const root = new URL('../../', import.meta.url)
const helpers = await import(new URL('adapters/dingtalk/helpers.ts', root).href)
const permission = await import(new URL('adapters/dingtalk/permission-card.ts', root).href)
const cards = await import(new URL('adapters/dingtalk/ai-card.ts', root).href)
const input = JSON.parse(await Bun.stdin.text())
const result: Record<string, unknown> = {}
if (input.payloads) result.payloads = input.payloads.map(raw => {
  const parsed = helpers.parseDingTalkPayload(raw)
  return { parsed, ...(parsed === null ? {} : {
    direct: helpers.isDingTalkDirectMessage(parsed), sender: helpers.getDingTalkSenderId(parsed),
    chat: helpers.getDingTalkChatId(parsed), text: helpers.extractDingTalkText(parsed), attachments: helpers.extractDingTalkAttachments(parsed),
  }) }
})
if (input.actions) result.actions = input.actions.map(permission.parseDingTalkPermissionCardAction)
if (input.params) result.params = input.params.map(v => permission.buildDingTalkPermissionCardParams(v.tool, v.input, v.request))
if (input.deliver) result.deliver = input.deliver.map(v => cards.buildDeliverBody(v.id, v.target, v.robot))
if (input.tables) {
  const source = readFileSync(fileURLToPath(new URL('adapters/dingtalk/ai-card.ts', root)), 'utf8')
  const start = source.indexOf('function ensureTableBlankLines(')
  if (start < 0) throw new Error('Source table boundary changed')
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(source.slice(start))
  const table = new Function(code + ';return ensureTableBlankLines')()
  result.tables = input.tables.map(table)
}
process.stdout.write(JSON.stringify(result))
