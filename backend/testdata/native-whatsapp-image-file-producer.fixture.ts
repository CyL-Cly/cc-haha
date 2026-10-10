import { createHash } from 'node:crypto'
import { createRequire } from 'node:module'
import { readFile, writeFile, mkdir } from 'node:fs/promises'
import { WriteStream } from 'node:fs'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { installWhatsAppImageFileProducer } from './whatsapp-image-file-producer'

const home = assertDisposableFixtureHome(process.env.HOME, 'WhatsApp image producer regression')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned image producer regression')
const adapterRequire = createRequire(new URL('../../adapters/package.json', import.meta.url))
const sdkEntry = adapterRequire.resolve('@whiskeysockets/baileys')
const leaf = (file: string) => join(dirname(sdkEntry), file)
const { WhatsAppMediaService } = await import('../../adapters/whatsapp/media.ts')
const { AttachmentStore } = await import('../../adapters/common/attachment/attachment-store.ts')
const { prepareWAMessageMedia } = await import(leaf('Utils/messages.js'))
const { extractImageThumb } = await import(leaf('Utils/messages-media.js'))
const { proto } = await import(leaf('../WAProto/index.js'))
const sharp = createRequire(sdkEntry)('sharp')
const input = await Bun.stdin.json(), data = Buffer.from(input.data, 'base64')
const sha = (bytes: Buffer) => createHash('sha256').update(bytes).digest('hex')
const direct = await extractImageThumb(data, 32)
const originalMetadata = sharp.prototype.metadata, originalEnd = WriteStream.prototype.end
const ready: any[] = [], warnings: any[] = []
const restore = installWhatsAppImageFileProducer(sdkEntry, (file, stream) => {
  ready.push({ file, writableFinished: stream.writableFinished, closed: stream.closed, bytesWritten: stream.bytesWritten, fd: stream.fd })
})
const logger = { trace() {}, debug() {}, info() {}, error() {}, warn(value: any) { warnings.push(value?.trace) } }
let result: unknown
const disk: any[] = []
try {
  const sock = { async sendMessage(_jid: string, payload: unknown) {
    const message = await prepareWAMessageMedia(payload, { logger, upload: async () => ({ mediaUrl: 'https://mmg.whatsapp.net/media', directPath: '/media' }) })
    result = proto.Message.toObject(message, { bytes: String, longs: String }).imageMessage
  } }
  // Observe the physical file at the controlled metadata boundary, before the
  // SDK finally unlinks it. This calls the same original Sharp function.
  const controlledMetadata = sharp.prototype.metadata
  sharp.prototype.metadata = function (...args: any[]) {
    const file = this.options?.input?.file
    const pending = controlledMetadata.apply(this, args)
    return pending.then(async (metadata: unknown) => {
      if (ready.some(row => row.file === file)) {
        const bytes = await readFile(file)
        disk.push({ file, length: bytes.length, sha256: sha(bytes) })
      }
      return metadata
    })
  }
  await new WhatsAppMediaService(sock as any, new AttachmentStore()).sendMedia('111@s.whatsapp.net', data, input.mime)
  const foreignDirectory = join(home, 'unrelated-files')
  await mkdir(foreignDirectory)
  const foreign = join(foreignDirectory, 'image-not-sdk-original')
  await writeFile(foreign, data)
  const unrelated = await sharp(foreign).metadata()
  const buffer = await sharp(data).metadata()
  process.stdout.write(JSON.stringify({ result, warnings, ready, disk, inputSHA256: sha(data), directSHA256: sha(direct.buffer), unrelated: { width: unrelated.width, height: unrelated.height }, buffer: { width: buffer.width, height: buffer.height } }) + '\n')
} finally {
  restore()
  if (sharp.prototype.metadata !== originalMetadata || WriteStream.prototype.end !== originalEnd) throw new Error('Image producer boundary was not restored')
}
