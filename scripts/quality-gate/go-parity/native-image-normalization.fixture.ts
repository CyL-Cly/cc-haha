import { assertDisposableFixtureHome } from './fixture-guard'
import { maybeResizeAndDownsampleImageBuffer, downsampleImageBufferToVisionTokenBudget, createImageMetadataText, compressImageBuffer } from '../../../src/utils/imageResizer'
assertDisposableFixtureHome(process.env.HOME, 'Native image normalization')
if (process.env.CC_HAHA_IMAGE_NORMALIZATION_FIXTURE !== '1') throw new Error('Unowned image normalization fixture')
const rows = await Bun.stdin.json()
const output = []
for (const row of rows) {
  try {
    const bytes = Buffer.from(row.data, 'base64')
    const compressed = row.maxBytes === undefined ? undefined : await compressImageBuffer(bytes, row.maxBytes, 'image/' + row.ext)
    const value = compressed ? { buffer: Buffer.from(compressed.base64, 'base64'), mediaType: compressed.mediaType.slice(6), dimensions: undefined } : row.tokens === undefined ? await maybeResizeAndDownsampleImageBuffer(bytes, bytes.length, row.ext) : await downsampleImageBufferToVisionTokenBudget(bytes, bytes.length, row.ext, row.tokens)
    output.push({ value: { data: value.buffer.toString('base64'), mediaType: value.mediaType, dimensions: value.dimensions }, metadata: value.dimensions ? createImageMetadataText(value.dimensions, row.path) : null })
  } catch (error) { output.push({ error: (error as Error).message }) }
}
process.stdout.write(JSON.stringify(output))
