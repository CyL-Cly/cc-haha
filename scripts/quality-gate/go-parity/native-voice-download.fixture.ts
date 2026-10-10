import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdir, writeFile, readFile } from 'node:fs/promises'
import { join } from 'node:path'
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.homeDir, 'Native Voice download')
if (process.env.CC_HAHA_VOICE_DOWNLOAD_FIXTURE !== '1') throw new Error('Missing Voice download fixture')
for (const url of input.asset.urls) if (new URL(url).hostname !== '127.0.0.1') throw new Error('Download fixture must use loopback')
const { downloadAsset } = await import('../../../src/server/services/voice/download/downloader')
const path = join(input.homeDir, 'downloads', 'payload.bin')
await mkdir(join(input.homeDir, 'downloads'), { recursive: true })
if (input.part !== undefined) await writeFile(`${path}.part`, Buffer.from(input.part, 'base64'))
let result: unknown, error: unknown
const progress: unknown[] = []
try { result = await downloadAsset(input.asset, path, { maxRetries: 0, progressIntervalMs: 0, onProgress: state => progress.push(state) }) } catch (failure) { error = (failure as any).failure ?? { message: (failure as Error).message } }
async function disk(path: string) { try { return (await readFile(path)).toString('base64') } catch { return null } }
const output={ result, error, completed: await disk(path), part: await disk(`${path}.part`), progress }
await new Promise<void>(resolve => process.stdout.write(JSON.stringify(output), () => resolve()))
process.exit(0)
