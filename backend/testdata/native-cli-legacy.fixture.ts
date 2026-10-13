import fs from 'node:fs'
import path from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.homeDir, 'Native CLI legacy cleanup Source')
if (process.env.CC_HAHA_INSTALLATION_SOURCE_FIXTURE !== '1') throw new Error('Missing cleanup Source fixture')
const npm = path.join(home, 'bin/npm')
const capabilities = await installClientFixtureCapabilities(home, {
  absentPaths: absentManagedSettingsFixturePaths(),
  privateExecutableFiles: [npm],
  approvedSpawn: request => (request.command === npm || request.command === 'npm') && (JSON.stringify(request.args) === '["config","get","prefix"]' || request.args.length === 3 && request.args[0] === 'uninstall' && request.args[1] === '-g' && ['@anthropic-ai/claude-code', 'claude-code-local'].includes(request.args[2])),
})
await import('../../preload')
const { cleanupNpmInstallations, cleanupShellAliases } = await import('../../src/utils/nativeInstaller/installer')
const { filterClaudeAliases, getShellConfigPaths } = await import('../../src/utils/shellConfig')
const observations = []
for (const profile of input.profiles) {
  const prefix = path.join(home, 'prefix'), config = path.join(home, profile.config ?? '.claude'), zdot = path.join(home, 'zdot')
  process.env.CLAUDE_CONFIG_DIR = config
  process.env.ZDOTDIR = profile.zdot ? zdot : ''
  for (const directory of [prefix, path.join(home, '.claude/local'), zdot, path.join(home, '.config/fish')]) fs.rmSync(directory, { recursive: true, force: true })
  for (const directory of [path.join(prefix, 'bin'), path.join(prefix, 'lib/node_modules/@anthropic-ai/claude-code'), path.join(prefix, 'lib/node_modules/claude-code-local'), zdot, path.join(home, '.config/fish')]) fs.mkdirSync(directory, { recursive: true })
  fs.writeFileSync(path.join(prefix, 'lib/node_modules/claude-code-local/keep.txt'), 'package data remains')
  if (profile.local) { fs.mkdirSync(path.join(home, '.claude/local'), { recursive: true }); fs.writeFileSync(path.join(home, '.claude/local/old.js'), 'legacy local') }
  if (profile.bin === 'file') fs.writeFileSync(path.join(prefix, 'bin/claude'), 'old npm command')
  if (profile.bin === 'directory') fs.mkdirSync(path.join(prefix, 'bin/claude'))
  if (profile.bin === 'symlink') fs.symlinkSync('../lib/node_modules/claude-code-local/keep.txt', path.join(prefix, 'bin/claude'))
  fs.writeFileSync(path.join(home, 'npm-profile.json'), JSON.stringify({ ...profile, prefix }))
  fs.writeFileSync(path.join(home, 'npm-calls.jsonl'), '')
  const lines = profile.lines.map((line: string) => line.replaceAll('<local>', path.join(config, 'local/claude')).replaceAll('<home>', home))
  const filtered = filterClaudeAliases(lines)
  const paths = getShellConfigPaths()
  for (const configFile of Object.values(paths)) {
    fs.rmSync(configFile, { recursive: true, force: true })
    if (profile.rc === 'missing') continue
    if (profile.rc === 'directory') fs.mkdirSync(configFile)
    else fs.writeFileSync(configFile, lines.join('\n'), { mode: 0o640 })
  }
  const npmResult = await cleanupNpmInstallations()
  const aliases = await cleanupShellAliases()
  const files = Object.values(paths).map(file => { try { return { path: file, text: fs.readFileSync(file, 'utf8'), mode: fs.statSync(file).mode & 0o777 } } catch { return { path: file, text: null, mode: null } } })
  const calls = fs.readFileSync(path.join(home, 'npm-calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(line => JSON.parse(line))
  observations.push({ filtered, npmResult, aliases, files, calls, localExists: fs.existsSync(path.join(home, '.claude/local')), binExists: fs.existsSync(path.join(prefix, 'bin/claude')), packageData: fs.readFileSync(path.join(prefix, 'lib/node_modules/claude-code-local/keep.txt'), 'utf8') })
}
await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ observations, blocked: capabilities.blocked }), error => error ? reject(error) : resolve()))
