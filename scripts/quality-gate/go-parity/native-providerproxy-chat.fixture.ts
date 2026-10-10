import { assertDisposableFixtureHome } from './fixture-guard'
import { anthropicToOpenaiChat } from '../../../src/server/proxy/transform/anthropicToOpenaiChat'
import { openaiChatToAnthropic, getChatResponseError } from '../../../src/server/proxy/transform/openaiChatToAnthropic'

assertDisposableFixtureHome(process.env.HOME, 'Provider Chat protocol')
Date.now = () => 1777000000123
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
process.stdout.write(JSON.stringify(rows.map((row: { kind: string, value: never, options: never }) => {
  try {
    return row.kind === 'request' ? anthropicToOpenaiChat(row.value, row.options)
      : row.kind === 'error' ? getChatResponseError(row.value)
      : openaiChatToAnthropic(row.value, 'requested-model')
  } catch (error) {
    return { error: { name: (error as Error).name, message: (error as Error).message } }
  }
})))
