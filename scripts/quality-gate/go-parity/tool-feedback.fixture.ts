import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import { join } from 'node:path'
import type { Tool, ToolUseContext } from '../../../src/Tool'
import type { AssistantMessage } from '../../../src/utils/messages'

type Step = { operation: string; raw?: string; inputSource?: string; toolName?: string; toolID?: string; sourceUUID?: string }
const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; steps: Step[] }
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('tool feedback fixture requires explicit disposable HOME/config')
}
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.homeDir)
bootstrap.setOriginalCwd(input.homeDir)
bootstrap.setProjectRoot(input.homeDir)
const marker = await import('../../../src/utils/unparsedToolInput')
const { runToolUse } = await import('../../../src/services/tools/toolExecution')
const { z } = await import('zod/v4')
let uuidCalls = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `fixture-id-${++uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(value?: string | number) { super(value === undefined ? 1_700_000_000_000 : value) }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
const results: unknown[] = []
for (const step of input.steps) {
  const toolInput = step.raw !== undefined ? marker.createUnparsedToolInput(step.raw) : JSON.parse(step.inputSource!)
  if (step.operation === 'create-marker') { results.push(toolInput); continue }
  if (step.operation === 'is-marker') { results.push(marker.isUnparsedToolInput(toolInput)); continue }
  if (step.operation !== 'invalid' && step.operation !== 'unknown') throw new Error(`unsupported tool feedback operation: ${step.operation}`)
  if (step.operation === 'invalid' && !marker.isUnparsedToolInput(toolInput)) throw new Error('invalid feedback fixture requires a recognized marker')
  const inputSchema = z.object({ optional: z.string().optional() })
  // These are tripwires around the actual TS executor. The Go side ports only
  // the resulting envelope; it does not claim to port execution/permissions.
  inputSchema.safeParse = () => { throw new Error('schema must not run for rejected provider input') }
  const tool = {
    name: step.toolName,
    inputSchema,
    validateInput: () => { throw new Error('tool validation must not run for rejected provider input') },
    call: () => { throw new Error('tool side effects must not run for rejected provider input') },
  } as unknown as Tool
  const use = { type: 'tool_use' as const, id: step.toolID!, name: step.toolName!, input: toolInput }
  const assistant = { uuid: step.sourceUUID, message: { id: 'provider-message' } } as AssistantMessage
  const context = { options: { tools: step.operation === 'invalid' ? [tool] : [], mcpClients: [] }, abortController: new AbortController(), messages: [] } as unknown as ToolUseContext
  const permission = async () => { throw new Error('permission checks must not run for rejected provider input') }
  const updates = []
  for await (const update of runToolUse(use, assistant, permission, context)) updates.push(update)
  if (updates.length !== 1) throw new Error(`expected exactly one rejection envelope, got ${updates.length}`)
  results.push(updates[0])
}
process.stdout.write(`${JSON.stringify({ results, uuidCalls })}\n`)
