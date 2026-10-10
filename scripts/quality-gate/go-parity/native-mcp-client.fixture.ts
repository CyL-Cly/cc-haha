import { assertDisposableFixtureHome } from './fixture-guard'

const home = process.env.HOME!
assertDisposableFixtureHome(home, 'Native MCP client')
if (process.env.CC_HAHA_MCP_CLIENT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') {
  throw new Error('MCP client fixture requires isolated production configuration')
}
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const schemas = await import('@modelcontextprotocol/sdk/types.js')
const resultSchemas = {
  'initialize': schemas.InitializeResultSchema,
  'tools/list': schemas.ListToolsResultSchema,
  'tools/call': schemas.CallToolResultSchema,
  'resources/list': schemas.ListResourcesResultSchema,
  'resources/templates/list': schemas.ListResourceTemplatesResultSchema,
  'resources/read': schemas.ReadResourceResultSchema,
  'prompts/list': schemas.ListPromptsResultSchema,
  'prompts/get': schemas.GetPromptResultSchema,
  'ping': schemas.EmptyResultSchema,
} as Record<string, any>
if (input.mode === 'validation') {
  const escape = (await import('cross-spawn/lib/util/escape')).default
  process.stdout.write(JSON.stringify({
    results: input.cases.map((item: any) => {
      const parsed = resultSchemas[item.method].safeParse(item.result)
      return parsed.success ? { ok: true, result: parsed.data, resultJSON: JSON.stringify(parsed.data) } : { ok: false }
    }),
    messages: input.messages.map((item: any) => schemas.JSONRPCMessageSchema.safeParse(item).success),
    windowsEscapes: input.windowsEscapes.map((value: string) => ({ command: escape.command(value), argument: escape.argument(value, false), double: escape.argument(value, true) })),
  }))
  process.exit(0)
}
await import('../../../preload')
// Use the original production connection owner and its actual installed SDK.
// No transport, fetch, process or connection method is mocked.
const { connectToServer, clearServerCache } = await import('../../../src/services/mcp/client')
const { normalizeNameForMCP } = await import('../../../src/services/mcp/normalization')
const { buildMcpToolName } = await import('../../../src/services/mcp/mcpStringUtils')
const config = { ...input.config, scope: 'user' }
const connection = await connectToServer('native-client-contract', config)
if (connection.type !== 'connected') {
  process.stdout.write(JSON.stringify({ connection: connection.type, error: connection.error ?? null }))
  process.exit(0)
}
const client = connection.client
const values: unknown[] = []
const progress: unknown[] = []
for (const request of input.requests) {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  if (request.cancelAfter) timer = setTimeout(() => controller.abort('contract cancel'), request.cancelAfter)
  try {
    const schema = resultSchemas[request.method] ?? schemas.ResultSchema
    const result = await client.request({ method: request.method, ...(request.params ? { params: request.params } : {}) }, schema as any, {
      signal: controller.signal,
      timeout: request.timeout ?? 1000,
      ...(request.progress ? { onprogress: (data: unknown) => progress.push(data) } : {}),
    })
    values.push({ result })
  } catch (error) {
    values.push({ error: { code: (error as any).code ?? null, message: (error as Error).message } })
  } finally {
    if (timer) clearTimeout(timer)
  }
}
const metadata = { capabilities: connection.capabilities, serverInfo: connection.serverInfo, instructions: client.getInstructions(), protocolVersion: (client.transport as any)?.protocolVersion ?? null }
await clearServerCache('native-client-contract', config)
process.stdout.write(JSON.stringify({ values, progress, metadata,
  resultJSON: values.map((value: any) => value.result ? JSON.stringify(value.result) : null),
  names: input.names.map((name: string) => normalizeNameForMCP(name)),
  toolName: buildMcpToolName('claude.ai Account 🧠', 'read.file'),
}))
