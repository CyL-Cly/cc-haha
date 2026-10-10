import * as fs from 'node:fs/promises'
import { isAbsolute, join, relative } from 'node:path'

type Step = { name: string; operation: 'read' | 'write' | 'mkdir' | 'rename'; path: string; target?: string }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; steps: Step[] }
if (!input.home || input.home !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.home, '.claude')) throw new Error('Native OS error fixture requires disposable HOME/config')
function path(name: string) {
  const value = join(input.home, name)
  const inside = relative(input.home, value)
  if (inside === '..' || inside.startsWith('../') || inside.startsWith('..\\') || isAbsolute(inside)) throw new Error('Native OS error fixture escaped HOME')
  return value
}
const results: Array<{ name: string; rendered: string; native: string }> = []
for (const step of input.steps) {
  let cause: unknown
  try {
    switch (step.operation) {
      case 'read': await fs.readFile(path(step.path)); break
      case 'write': await fs.writeFile(path(step.path), 'fixture'); break
      case 'mkdir': await fs.mkdir(path(step.path), { recursive: true }); break
      case 'rename': await fs.rename(path(step.path), path(step.target!)); break
    }
  } catch (error) { cause = error }
  if (!cause) throw new Error(`Native OS fixture unexpectedly succeeded: ${step.name}`)
  results.push({ name: step.name, rendered: String(cause), native: String(cause) })
}
console.log(JSON.stringify(results))
