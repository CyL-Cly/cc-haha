import { assertDisposableFixtureHome } from './fixture-guard'
assertDisposableFixtureHome(process.env.HOME, 'Image generation pure contracts')
if (process.env.CC_HAHA_IMAGEGEN_PURE_FIXTURE !== '1') throw new Error('Unowned image generation fixture')
const rows = JSON.parse(await new Response(Bun.stdin.stream()).text())
const backend = await import('../../../src/tools/ImageGenTool/backend')
const apiSmart = await import('../../../src/tools/ImageGenTool/apiSmart')
const config = await import('../../../src/services/imageGeneration/config')
const output = []
for (const row of rows) {
  try {
    if (row.model === undefined) delete process.env.ANTHROPIC_MODEL
    else process.env.ANTHROPIC_MODEL = row.model
    const images = (row.images ?? []).map((image: any) => ({ ...image, bytes: Buffer.alloc(image.size ?? 0) }))
    let value
    switch (row.kind) {
      case 'config': value = config.getImageGenerationRuntimeConfig(row.env); break
      case 'chat': value = backend.buildChatGPTRequestBody(row.input, images); break
      case 'grok': value = images.length ? backend.buildGrokEditRequestBody(row.input, images) : backend.buildGrokRequestBody(row.input); break
      case 'compatible': value = backend.buildCompatibleRequestBody(row.input); break
      case 'url': value = row.operation === 'edits' ? backend.buildImagesEditUrl(row.base) : backend.buildImagesGenerationUrl(row.base); break
      case 'smart': value = apiSmart.buildApiSmartImageBodies({ ...row.input, count: 1 }, images)[0]; break
      case 'is-smart': value = apiSmart.isApiSmartImageConfig(row.config); break
      case 'stream': value = backend.parseChatGPTImageStream(row.stream); break
      case 'response': value = backend.parseCompatibleImageResponse(row.value); break
      default: throw new Error('Unowned pure image profile')
    }
    output.push({ value })
  } catch (error) { output.push({ error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify(output))
