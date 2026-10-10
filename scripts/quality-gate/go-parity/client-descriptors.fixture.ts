import fs, { appendFileSync, closeSync, openSync, readFileSync, writeSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { homeDir: string; outside: string; mode: string }
assertDisposableFixtureHome(input.homeDir, 'Client descriptors')
if (process.env.CC_HAHA_CLIENT_DESCRIPTORS_FIXTURE !== '1') throw new Error('Descriptor fixture marker missing')
// Only fixture setup retains this opener. It proves pre-existing / reused
// external descriptors receive no capability from a numeric fd coincidence.
const setupOpen = fs.openSync.bind(fs)
const outsideFD = setupOpen(input.outside, 'r+')
const capabilities = await installClientFixtureCapabilities(input.homeDir)
const path = join(input.homeDir, 'transcript.jsonl')
if (input.mode === 'private') {
  const fd = openSync(path, 'a+')
  appendFileSync(fd, 'first\n')
  fs.appendFileSync(fd, 'second\n')
  writeSync(fd, 'third\n')
  closeSync(fd)
  process.stdout.write(JSON.stringify({ text: readFileSync(path, 'utf8'), capabilities }))
} else if (input.mode === 'unknown') {
  appendFileSync(outsideFD, 'escape')
} else if (input.mode === 'reused') {
  const fd = openSync(path, 'a+')
  closeSync(fd)
  const reused = setupOpen(input.outside, 'r+')
  if (fd !== reused) throw new Error('OS did not reuse the closed descriptor; probe did not execute')
  appendFileSync(reused, 'escape')
} else if (input.mode === 'readonly') {
  const fd = openSync(path, 'r')
  appendFileSync(fd, 'escape')
} else if (input.mode === 'symlink') {
  openSync(join(input.homeDir, 'alias'), 'a')
} else throw new Error('Unknown descriptor fixture mode')
