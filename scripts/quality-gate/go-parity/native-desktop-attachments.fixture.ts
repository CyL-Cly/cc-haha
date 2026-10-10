import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'Desktop attachment producer')
if (process.env.CC_HAHA_DESKTOP_ATTACHMENTS_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || !Array.isArray(input.cases)) throw new Error('Unowned desktop attachment fixture')
const source = fs.readFileSync(new URL('../../../src/server/services/conversationService.ts', import.meta.url), 'utf8')
const start = source.indexOf('  private async buildUserContent(')
const end = source.indexOf('  private getSdkTokenFromUrl(', start)
if (start < 0 || end < 0) throw new Error('Original desktop attachment production methods missing')
// Extract unchanged original production method bodies; no service constructor,
// live provider, Node worker, or developer configuration is involved.
const code = new Bun.Transpiler({ loader: 'ts' }).transformSync('class AttachmentSource {\n' + source.slice(start, end) + '\n}\n')
let sequence = 0
const Source = new Function('fs', 'os', 'path', 'crypto', 'maybeResizeAndDownsampleImageBuffer', 'createImageMetadataText', 'logError', code + '; return AttachmentSource')(
  fs, os, path, { randomUUID: () => '00000000-0000-4000-8000-' + String(++sequence).padStart(12, '0') },
  () => { throw new Error('Image transforms require a separate native producer fixture') },
  () => { throw new Error('Unexpected image metadata') }, () => {},
)
const service = new Source()
const results = []
for (const row of input.cases) {
  if ((row.attachments ?? []).some((attachment: any) => service.shouldInlineImageAttachment(attachment))) throw new Error('Image scenario must use the actual image producer fixture')
  const blocks = await service.buildUserContent(row.content, row.sessionId, row.attachments)
  const uploadDir = path.join(home, '.claude', 'uploads', row.sessionId)
  const files = fs.existsSync(uploadDir) ? fs.readdirSync(uploadDir).sort().map(name => ({ name, data: fs.readFileSync(path.join(uploadDir, name)).toString('base64') })) : []
  results.push({ blocks, files })
}
process.stdout.write(JSON.stringify({ results }) + '\n')
