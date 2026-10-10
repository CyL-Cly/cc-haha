import { extractLocalCommand, extractLocalCommandOutput, extractGoalEvent } from '../../../../src/server/ws/cliMessageParsing.ts'

const rows = JSON.parse(await Bun.stdin.text())
const results = rows.map((row: { content: unknown; untagged: boolean }) => {
  const command = extractLocalCommand(row.content)
  const output = extractLocalCommandOutput(row.content, { allowUntagged: row.untagged })
  return { command, output, goal: output ? extractGoalEvent(output, command ?? undefined) : null }
})
console.log(JSON.stringify(results))
