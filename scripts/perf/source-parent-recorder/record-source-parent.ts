import { createHash } from 'node:crypto'
import { closeSync, existsSync, lstatSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { PAIRED_PROTOCOL } from '../go-main-paired-benchmark-core'
import { freezeResolvedDependencies, verifyCandidateEvidence } from '../go-main-paired-benchmark-evidence'
import { command } from './formal-command'
import { startParentTransport } from './formal-transport'

const args = process.argv.slice(2)
const arg = (name: string) => {
  const index = args.indexOf(name)
  if (index < 0 || !args[index + 1] || args[index + 1].startsWith('--')) throw new Error('Missing ' + name)
  return realpathSync(args[index + 1])
}
const sha = (bytes: string | Uint8Array) => createHash('sha256').update(bytes).digest('hex')
const allocation = arg('--allocation'), source = arg('--source-root'), binary = arg('--go-binary')
const candidateFile = arg('--candidate-evidence')
const descriptor = (name: string) => {
  const index = args.indexOf(name)
  const value = Number(args[index + 1])
  if (index < 0 || !Number.isSafeInteger(value) || value <= 2) throw new Error('Private inherited descriptor required')
  return value
}
const readyFd = descriptor('--ready-fd'), credentialFd = descriptor('--credential-fd')
if (readyFd === credentialFd) throw new Error('Distinct private descriptors required')
if (!args.includes('--allow-live')) throw new Error('Explicit --allow-live required')
if (!allocation.startsWith('/private/tmp/') || (lstatSync(allocation).mode & 0o777) !== 0o700) throw new Error('Private canonical allocation required')
const root = resolve(import.meta.dir, '../../..')
const wrapper = join(root, 'scripts/perf/go-main-source-live.ts')
const candidate = verifyCandidateEvidence(candidateFile, sha(readFileSync(binary)), process.platform + '/' + process.arch)
const dependencies = freezeResolvedDependencies(source)
for (const [name, expected] of [['@anthropic-ai/sdk', '0.128.0'], ['axios', '1.15.2']]) {
  if (JSON.parse(readFileSync(join(source, 'node_modules', name, 'package.json'), 'utf8')).version !== expected) throw new Error('Pinned Source dependency mismatch')
}
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Pinned Bun mismatch')
// The same worker admits the credential only after actual validation. Python
// reads one K1 after this exact READY and sends it via a separate anonymous pipe.
writeFileSync(readyFd, 'SOURCE_PARENT_VALIDATED_V2\n')
closeSync(readyFd)
const key = readFileSync(credentialFd, 'utf8')
closeSync(credentialFd)
if (!key || key.length > 4096 || !/^[\x21-\x7e]+$/.test(key) || /^[{\[]/.test(key)) throw new Error('key-file requires raw token, not settings JSON')
const redact = (text: string) => text.replaceAll(key, '<redacted-key>')
const home = join(allocation, 'source-home'), evidence = join(allocation, 'transport')
if (existsSync(home) || existsSync(evidence)) throw new Error('Never reuse a partial parent pilot')
mkdirSync(join(home, '.claude'), { recursive: true, mode: 0o700 })
mkdirSync(join(home, 'tmp'), { mode: 0o700 })
mkdirSync(evidence, { mode: 0o700 })
writeFileSync(join(evidence, 'requests.jsonl'), '', { mode: 0o600, flag: 'wx' })
writeFileSync(join(evidence, 'transport.jsonl'), '', { mode: 0o600, flag: 'wx' })
writeFileSync(join(home, '.claude/settings.json'), '{"disableAllHooks":true,"maxConcurrentSubagents":2}', { mode: 0o600, flag: 'wx' })
const transport = startParentTransport(evidence, key)
const env = {
  HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: home + '/.claude', TMPDIR: home + '/tmp', CLAUDE_CODE_TMPDIR: home + '/tmp',
  XDG_CONFIG_HOME: home + '/.config', XDG_CACHE_HOME: home + '/.cache', XDG_DATA_HOME: home + '/.local/share',
  PATH: dirname(process.execPath) + ':/opt/homebrew/bin:/usr/bin:/bin', SHELL: '/bin/bash',
  BUN_OPTIONS: '--no-env-file', NODE_ENV: 'production', USER_TYPE: 'external',
  CC_HAHA_DISABLE_TERMINAL_SHELL_ENV: '1', DISABLE_TELEMETRY: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1',
  CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH: '1', CLAUDE_CODE_DISABLE_AUTO_MEMORY: 'true',
  ANTHROPIC_API_KEY: key, ANTHROPIC_BASE_URL: transport.origin + '/anthropic',
  ANTHROPIC_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_HAIKU_MODEL: PAIRED_PROTOCOL.model,
  ANTHROPIC_DEFAULT_SONNET_MODEL: PAIRED_PROTOCOL.model, ANTHROPIC_DEFAULT_OPUS_MODEL: PAIRED_PROTOCOL.model,
  CLAUDE_CODE_MODEL_CONTEXT_WINDOWS: JSON.stringify({ [PAIRED_PROTOCOL.model]: PAIRED_PROTOCOL.contextWindow }),
  CLAUDE_CODE_MAX_OUTPUT_TOKENS: String(PAIRED_PROTOCOL.maxOutputTokens), CLAUDE_CODE_MAX_RETRIES: '2',
  CC_HAHA_SEND_DISABLED_THINKING: '1',
  ENABLE_TOOL_SEARCH: 'false', CC_HAHA_PAIRED_ALLOW_LIVE: '1', CC_HAHA_PAIRED_SOURCE_ROOT: source,
  CC_HAHA_PAIRED_ALLOCATION: allocation,
}
const prompt = 'Reply with exactly SOURCE_LIVE_FULL_READY and no other text. Do not use tools.'
const argv = [process.execPath, '--no-env-file', wrapper, '--print', '--model', PAIRED_PROTOCOL.model,
  '--thinking', PAIRED_PROTOCOL.thinking, '--no-computer-use', '--dangerously-skip-permissions',
  '--output-format', 'stream-json', '--verbose', '--max-turns', String(PAIRED_PROTOCOL.maxTurns), prompt]
let result: Awaited<ReturnType<typeof command>> | undefined
let failure: string | undefined
try {
  result = await command(argv, home, env, PAIRED_PROTOCOL.timeoutMs)
} catch (error) {
  failure = redact(String(error))
} finally {
  await transport.close()
}
const state = transport.snapshot()
let outputRedactions = 0
for (const name of ['stdout', 'stderr'] as const) {
  const raw = result?.[name] ?? ''
  if (raw.includes(key)) outputRedactions++
  writeFileSync(join(allocation, name === 'stdout' ? 'stdout.jsonl' : 'stderr.log'), redact(raw), { flag: 'wx', mode: 0o600 })
}
const candidateAfter = verifyCandidateEvidence(candidateFile, sha(readFileSync(binary)), process.platform + '/' + process.arch)
const dependenciesAfter = freezeResolvedDependencies(source)
if (JSON.stringify(candidateAfter) !== JSON.stringify(candidate) || dependenciesAfter.hash !== dependencies.hash) failure = 'Candidate/dependency inputs changed during parent pilot'
// Never serialize env: it contains the authorized credential.
const receipt = { scope: 'real-Source-unscored-parent-pilot-not-120-benchmark', argv, cwd: home, prompt, candidate,
  source, dependenciesHash: dependencies.hash, wrapperHash: sha(readFileSync(wrapper)), binaryHash: sha(readFileSync(binary)),
  bun: { path: process.execPath, version: Bun.version, revision: Bun.revision, sha256: sha(readFileSync(process.execPath)) },
  process: result && { ...result, stdout: undefined, stderr: undefined }, transport: state, outputRedactions, failure }
writeFileSync(join(allocation, 'pilot-process.json'), redact(JSON.stringify(receipt, null, 2)) + '\n', { flag: 'wx', mode: 0o600 })
if (failure || !result || result.code !== 0 || result.timedOut || !result.joined || !result.stdoutComplete || !result.stderrComplete || result.recordingErrors.length || state.redactions || outputRedactions || state.errors.length || state.recordingErrors.length || state.modelCalls < 1) throw new Error('Parent pilot failed; preserve this allocation, do not freeze transcript')
console.log(JSON.stringify({ status: 'real-source-pilot-complete-await-python-freeze', allocation }))
