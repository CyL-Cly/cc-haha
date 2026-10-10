import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Image tools contracts')
if (process.env.CC_HAHA_IMAGEGEN_TOOL_FIXTURE !== '1') throw new Error('Unowned image tool fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { ImageGenTool, ImageEditTool } = await import('../../../src/tools/ImageGenTool/ImageGenTool')
const { zodToJsonSchema } = await import('../../../src/utils/zodToJsonSchema')
const output = []
for (const row of rows) {
  const tool = row.name === 'ImageGen' ? ImageGenTool : ImageEditTool
  if (row.env) {
    for (const key of ['CC_HAHA_IMAGE_PROVIDER_KIND', 'CC_HAHA_IMAGE_PROVIDER_ID', 'CC_HAHA_IMAGE_MODEL', 'CC_HAHA_IMAGE_BASE_URL', 'CC_HAHA_IMAGE_API_KEY']) {
      if (row.env[key] === undefined) delete process.env[key]
      else process.env[key] = row.env[key]
    }
    output.push({ enabled: tool.isEnabled() })
    continue
  }
  const schema = zodToJsonSchema(tool.inputSchema)
  let parsed
  try { parsed = { value: tool.inputSchema.parse(row.input) } }
  catch (error) { parsed = { issues: (error as any).issues } }
  output.push({ description: await tool.description(), prompt: await tool.prompt(), searchHint: tool.searchHint, strict: tool.strict, shouldDefer: tool.shouldDefer, schema, parsed })
}
process.stdout.write(JSON.stringify(output))
