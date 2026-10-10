import { parse } from 'acorn'
import { readFile } from 'node:fs/promises'

// Execute the original print consumer's retention predicate and JSON branch.
// This isolates output formatting only; actual public CLI/physical provider
// consumers are separate tests. No replacement QueryEngine or runtime state.
if (process.env.CC_HAHA_PUBLIC_PRINT_JSON_SOURCE !== '1') throw new Error('Missing original print JSON fixture guard')
const source = await readFile(new URL('../../../src/cli/print.ts', import.meta.url), 'utf8')
const code = new Bun.Transpiler({ loader: 'ts', target: 'bun' }).transformSync(source)
const tree = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const nodes: any[] = []
function visit(node: any) {
  if (!node || typeof node !== 'object') return
  if (typeof node.type === 'string') nodes.push(node)
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) for (const child of value) visit(child)
    else if (value && typeof value === 'object') visit(value)
  }
}
visit(tree)
const retention = nodes.filter(node => node.type === 'IfStatement'
  && code.slice(node.test.start, node.test.end).includes('message.type')
  && code.slice(node.consequent.start, node.consequent.end).includes('messages.push(message)')
  && code.slice(node.consequent.start, node.consequent.end).includes('lastMessage = message'))
const formatting = nodes.filter(node => node.type === 'SwitchStatement'
  && code.slice(node.discriminant.start, node.discriminant.end) === 'options.outputFormat'
  && code.slice(node.start, node.end).includes('jsonStringify(messages)'))
if (retention.length !== 1 || formatting.length !== 1) throw new Error('Original print JSON consumer shape changed')
const predicate = code.slice(retention[0].test.start, retention[0].test.end)
const jsonCase = formatting[0].cases.find((node: any) => node.test?.value === 'json')
if (!jsonCase) throw new Error('Missing original public JSON case')
const body = code.slice(jsonCase.start, jsonCase.end)
const retain = new Function('message', `return (${predicate})`) as (message: any) => boolean
const emit = new Function('options', 'messages', 'lastMessage', `
  let stdout = ''
  const writeToStdout = value => { stdout += value }
  const jsonStringify = JSON.stringify
  switch (options.outputFormat) { ${body} }
  return stdout
`) as (options: any, messages: any[], last: any) => string
const input = JSON.parse(await Bun.stdin.text()) as { rows: Array<{ verbose: boolean; messages: any[] }> }
const observed = input.rows.map(row => {
  const retained = row.messages.filter(retain)
  try {
    return { stdout: emit({ outputFormat: 'json', verbose: row.verbose }, row.verbose ? retained : [], retained.at(-1)) }
  } catch (error) {
    return { error: error instanceof Error ? error.message : String(error) }
  }
})
console.log(JSON.stringify(observed))
