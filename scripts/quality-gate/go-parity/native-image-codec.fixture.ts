import { assertDisposableFixtureHome } from './fixture-guard'
import { getImageProcessor } from '../../../src/tools/FileReadTool/imageProcessor'
assertDisposableFixtureHome(process.env.HOME, 'Native image codec parity')
if (process.env.CC_HAHA_IMAGE_CODEC_FIXTURE !== '1') throw new Error('Unowned native image codec fixture')
const sharp = await getImageProcessor()
const rows = await Bun.stdin.json()
const output = []
for (const row of rows) {
  let data = Buffer.from(row.data, 'base64')
  if (row.profile === 'p3') data = await sharp(data).withIccProfile('p3').png().toBuffer()
  if (row.profile === 'cmyk') data = await sharp(data).toColourspace('cmyk').jpeg({ quality: 90 }).toBuffer()
  const metadata = await sharp(data).metadata()
  let pipeline = sharp(data)
  if (row.width && row.height && (metadata.width !== row.width || metadata.height !== row.height)) pipeline = pipeline.resize(row.width, row.height, { fit: 'inside', withoutEnlargement: true })
  if (row.format === 'png') pipeline = pipeline.png({ compressionLevel: 9 })
  else if (row.format === 'webp') pipeline = pipeline.webp({ lossless: true })
  else pipeline = pipeline.jpeg({ quality: row.quality })
  const encoded = await pipeline.toBuffer()
  output.push({ metadata: { Width: metadata.width, Height: metadata.height, Format: metadata.format }, data: encoded.toString('base64'), ...(row.profile ? { inputData: data.toString('base64') } : {}), ...(row.format === 'webp' ? { decodedPng: (await sharp(encoded).png({ compressionLevel: 9 }).toBuffer()).toString('base64') } : {}) })
}
process.stdout.write(JSON.stringify(output))
