import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { mkdir, writeFile, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { createHash } from 'node:crypto'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Voice contract')
if (process.env.CC_HAHA_VOICE_CONTRACT_FIXTURE !== '1') throw new Error('Missing native Voice fixture')
const capabilities = await installClientFixtureCapabilities(input.homeDir, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../preload')
const { installItems, resolveRuntimePlatform } = await import('../../../src/server/services/voice/sensevoice/assets')
const { validateVoiceWav } = await import('../../../src/server/services/voice/wav')
const { readSamples } = await import('../../../src/server/services/voice/sensevoice/wav')
const manifest = await import('../../../src/server/services/voice/sensevoice/manifest')
const assets = []
for (const [platform, arch, source] of input.assets) assets.push(installItems(resolveRuntimePlatform(platform, arch), source))
const wav = input.wav.map((encoded: string) => {
 const bytes = new Uint8Array(Buffer.from(encoded, 'base64'))
 let validated: unknown, samples: unknown
 try { validated = validateVoiceWav(bytes) } catch (error) { validated = { message: (error as Error).message, code: (error as any).code, status: (error as any).status } }
 try { const values=readSamples(bytes); samples={length:values.length,first:[...values.slice(0,8)],last:[...values.slice(-8)],sha256:createHash('sha256').update(Buffer.from(values.buffer)).digest('hex')} } catch (error) { samples = { message: (error as Error).message } }
 return { validated, samples }
})
const path = join(input.homeDir, 'manifest', 'verified.json')
await mkdir(join(input.homeDir, 'manifest'), { recursive: true })
await writeFile(path, input.manifest)
const before = await manifest.readManifest(path)
await manifest.recordVerified(path, input.entries)
const after = await manifest.readManifest(path)
const disk = JSON.parse(await readFile(path, 'utf8'))
await new Promise<void>(resolve=>process.stdout.write(JSON.stringify({ assets, wav, before, after, disk, capabilities }),()=>resolve()))
process.exit(0)
