import { WriteStream } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { basename, dirname, resolve } from 'node:path'
import { finished } from 'node:stream/promises'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

// Stock encryptedStream ends its temporary WriteStream without joining it. The
// oracle controls that external filesystem producer: metadata must consume the
// completed original file. The SDK and Sharp functions themselves stay intact.
// This is not a claim that stock's unjoined timing has the same behavior.
export function installWhatsAppImageFileProducer(sdkEntry: string, onReady?: (file: string, stream: WriteStream) => void) {
  const home = assertDisposableFixtureHome(process.env.HOME, 'WhatsApp image file producer')
  if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1' || resolve(tmpdir()) !== resolve(home)) throw new Error('Unowned WhatsApp image file producer')
  const sharp = createRequire(sdkEntry)('sharp')
  const metadata = sharp.prototype.metadata
  const end = WriteStream.prototype.end
  const producers = new Map<string, { stream: WriteStream, completion: Promise<void> }>()
  const owned = (file: unknown): file is string => typeof file === 'string' && dirname(resolve(file)) === resolve(home) && /^image3EB0[0-9A-F]{18}-original$/.test(basename(file))
  WriteStream.prototype.end = function (...args: any[]) {
    if (owned(this.path)) {
      const completion = finished(this, { cleanup: true })
      // Preserve a failed producer for the metadata consumer without an early
      // unhandled rejection before that consumer reaches the file.
      void completion.catch(() => {})
      producers.set(resolve(this.path), { stream: this, completion })
    }
    return end.apply(this, args as any)
  } as any
  sharp.prototype.metadata = function (...args: any[]) {
    const file = this.options?.input?.file
    if (!owned(file)) return metadata.apply(this, args)
    const producer = producers.get(resolve(file))
    if (!producer) throw new Error('SDK original image producer was not observed')
    return producer.completion.then(() => {
      if (!producer.stream.writableFinished || !producer.stream.closed) throw new Error('SDK original image producer did not finish and close')
      onReady?.(file, producer.stream)
      return metadata.apply(this, args)
    })
  }
  return () => {
    sharp.prototype.metadata = metadata
    WriteStream.prototype.end = end
  }
}
