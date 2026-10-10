import { assertDisposableFixtureHome } from './fixture-guard'
import { readFileSync } from 'node:fs'
import { parse } from 'acorn'

assertDisposableFixtureHome(process.env.HOME, 'Proxy native HTTP lifecycle')
const source = new Bun.Transpiler({ loader: 'ts' }).transformSync(readFileSync(new URL('../../../src/server/proxy/handler.ts', import.meta.url), 'utf8'))
const ast = parse(source, { ecmaVersion: 'latest', sourceType: 'module' }) as any
const names = ['createTimeoutController', 'fetchUpstreamWithTimeout', 'withStreamIdleTimeout']
const declarations = names.map(name => {
  const node = ast.body.map((node: any) => node.declaration ?? node).find((node: any) => node.type === 'FunctionDeclaration' && node.id?.name === name)
  if (!node) throw new Error(`Missing actual producer ${name}`)
  return source.slice(node.start, node.end)
})
const api = new Function(`${declarations.join('\n')}\nreturn { fetchUpstreamWithTimeout, withStreamIdleTimeout }`)()
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const output = []
for (const row of rows) {
  const timers: ReturnType<typeof setTimeout>[] = []
  const peer = Bun.serve({ port: 0, hostname: '127.0.0.1', fetch() {
    return new Response(new ReadableStream({ start(controller) {
      const send = (text: string, at: number) => timers.push(setTimeout(() => { try { controller.enqueue(new TextEncoder().encode(text)) } catch {} }, at))
      send('prefix', 0)
      if (row.mode === 'trickle') {
        for (let index = 1; index <= 5; index++) send(String(index), index * 20)
        timers.push(setTimeout(() => { try { controller.close() } catch {} }, 110))
      }
    } }))
  } })
  let text = ''
  try {
    const response = await api.fetchUpstreamWithTimeout(`http://127.0.0.1:${peer.port}`, { method: 'POST', body: '{}' }, 70, row.stream)
    const body = row.stream ? api.withStreamIdleTimeout(response.body, 70) : response.body
    const reader = body.getReader()
    for (;;) {
      const next = await reader.read()
      if (next.done) break
      text += new TextDecoder().decode(next.value)
      if (row.mode === 'cancel') { await reader.cancel('private cancellation'); break }
    }
    output.push({ text })
  } catch (error) {
    output.push({ text, error: { name: (error as Error).name, message: (error as Error).message } })
  } finally { for (const timer of timers) clearTimeout(timer); peer.stop(true) }
}
process.stdout.write(JSON.stringify(output))
