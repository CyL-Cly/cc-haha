import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'

const input = await Bun.stdin.json()
assertDisposableFixtureHome(input.homeDir, 'WhatsApp actual message wire')
if (process.env.CC_HAHA_WHATSAPP_LEAF !== '1') throw new Error('Unowned message wire fixture')
const require = createRequire(new URL('../../../../adapters/package.json', import.meta.url))
const leaf = (name: string) => join(dirname(require.resolve('@whiskeysockets/baileys')), name)
const { proto } = await import(leaf('../WAProto/index.js'))
const bytes = Buffer.from([0, 1, 128, 255]).toString('base64')
const key = { remoteJid: '123-456@g.us', fromMe: false, id: 'original-quoted-id', participant: '111@s.whatsapp.net' }
const profiles = [
  { name: 'quoted-group-text', data: { extendedTextMessage: { text: 'actual quoted group reply', contextInfo: { stanzaId: 'original-quoted-id', participant: '111@s.whatsapp.net', remoteJid: '123-456@g.us', quotedMessage: { conversation: 'older text' }, mentionedJid: ['222@s.whatsapp.net'], expiration: 3600 } } } },
  { name: 'view-once-image', data: { viewOnceMessage: { message: { imageMessage: { url: 'https://example.invalid/private-media', mimetype: 'image/png', caption: '图片 — original caption', fileSha256: bytes, fileEncSha256: bytes, mediaKey: bytes, fileLength: '4096', height: 640, width: 480, directPath: '/test-only/media', jpegThumbnail: bytes, viewOnce: true } } } } },
  { name: 'original-document', data: { documentMessage: { url: 'https://example.invalid/private-document', mimetype: 'application/pdf', title: 'original title', fileName: '中文合同.pdf', fileLength: '8192', fileSha256: bytes, fileEncSha256: bytes, mediaKey: bytes, pageCount: 4, directPath: '/test-only/document', caption: '原版附件' } } },
  { name: 'voice-note', data: { audioMessage: { mimetype: 'audio/ogg; codecs=opus', fileLength: '512', seconds: 3, ptt: true, mediaKey: bytes, fileSha256: bytes, waveform: bytes, directPath: '/test-only/audio' } } },
  { name: 'ephemeral-video', data: { ephemeralMessage: { message: { videoMessage: { mimetype: 'video/mp4', fileLength: '1024', seconds: 6, width: 1920, height: 1080, caption: 'original video', gifPlayback: true, jpegThumbnail: bytes, mediaKey: bytes, contextInfo: { expiration: 86400 } } } } } },
  { name: 'edit-protocol', data: { protocolMessage: { type: proto.Message.ProtocolMessage.Type.MESSAGE_EDIT, key, editedMessage: { conversation: 'corrected original message' }, timestampMs: '1700000000123' } } },
  { name: 'reaction-protocol', data: { reactionMessage: { key, text: '✅', senderTimestampMs: '1700000000456' } } },
]
const options = { bytes: String, longs: String, enums: String }
const rows = input.wires
  ? input.wires.map((wire: string, index: number) => ({ name: profiles[index].name, expected: proto.Message.toObject(proto.Message.decode(Buffer.from(wire, 'base64')), options) }))
  : profiles.map(profile => {
    const message = proto.Message.fromObject(profile.data)
    return { name: profile.name, wire: Buffer.from(proto.Message.encode(message).finish()).toString('base64'), expected: proto.Message.toObject(message, options) }
  })
process.stdout.write(JSON.stringify(rows) + '\n')
