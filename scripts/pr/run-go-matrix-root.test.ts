import { expect, test } from 'bun:test'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

test('matrix CLI accepts its own artifact Poppler root while still failing absent native target evidence', async () => {
  const artifacts = resolve(import.meta.dir, '../../artifacts')
  mkdirSync(artifacts, { recursive: true })
  const owned = mkdtempSync(join(artifacts, 'matrix-poppler-root-regression-'))
  const root = join(owned, 'repository')
  const lane = mkdtempSync(join(tmpdir(), 'matrix-poppler-narrow-temp-'))
  const bin = join(root, 'artifacts/poppler/bin')
  const reports = join(owned, 'reports')
  const home = join(lane, 'home')
  for (const path of [join(root, 'backend'), join(root, 'scripts/quality-gate'), bin, reports, home]) mkdirSync(path, { recursive: true })
  const inputs = ['backend/main.go', 'backend/go.mod', 'scripts/quality-gate/coverage-thresholds.json']
  writeFileSync(join(root, inputs[0]), 'package fixture\n')
  writeFileSync(join(root, inputs[1]), 'module fixture.test/matrix-root\n\ngo 1.25.7\n')
  writeFileSync(join(root, inputs[2]), JSON.stringify({ changedLines: { minimumPercent: 90 } }))
  const calls = join(owned, 'primitive-calls.jsonl')
  const preload = join(owned, 'git-primitive-preload.ts')
  // Only the Git process primitive is substituted. Matrix CLI/root handling,
  // strict environment validation, real file snapshots and closure validation
  // execute unchanged. Any attempted Go invocation fails this fixture.
  writeFileSync(preload, `
    import {appendFileSync} from 'node:fs'
    Bun.spawn = ((command, options) => {
      if (!Array.isArray(command) || command[0] !== 'git') throw new Error('Matrix fixture must not invoke Go or another process')
      if (options.cwd !== ${JSON.stringify(root)} || options.env.CC_HAHA_TEST_POPPLER_BIN !== ${JSON.stringify(realpathSync(bin))}) throw new Error('Matrix failed to bind its exact artifact root')
      appendFileSync(${JSON.stringify(calls)}, JSON.stringify(command) + '\\n')
      let stdout = ''
      if (command.includes('merge-base')) stdout = 'a'.repeat(40) + '\\n'
      else if (command.includes('--cached')) stdout = ${JSON.stringify(inputs.join('\0') + '\0')}
      else if (!command.includes('diff') && !command.includes('ls-files')) throw new Error('Unexpected Git command')
      return {exited: Promise.resolve(0), stdout: new Response(stdout).body, stderr: new Response('').body}
    }) as any
  `)
  try {
    const child = Bun.spawn([process.execPath, '--no-env-file', '--preload', preload, resolve(import.meta.dir, 'run-go-matrix.ts'), '--reports-dir', reports], {
      cwd: root,
      env: { HOME: home, USERPROFILE: home, CLAUDE_CONFIG_DIR: join(home, '.claude'), TMPDIR: lane, TMP: lane, TEMP: lane, CC_HAHA_TEST_POPPLER_BIN: bin, GOTELEMETRY: 'off', NODE_ENV: 'production', ...(process.env.SYSTEMROOT ? { SYSTEMROOT: process.env.SYSTEMROOT } : {}) },
      stdin: 'ignore', stdout: 'pipe', stderr: 'pipe',
    })
    const [exitCode, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()])
    expect(exitCode).toBe(1)
    expect(stderr).not.toContain('Physical Poppler')
    expect(stderr).not.toContain('Matrix failed to bind')
    expect(stdout).toContain('Go all-target closure: failed')
    const result = JSON.parse(readFileSync(join(root, 'artifacts/go-matrix/report.json'), 'utf8'))
    expect(result.status).toBe('failed')
    expect(result.minimumPercent).toBe(90)
    expect(result.failures).toHaveLength(6)
    expect(result.failures.every((failure: string) => failure.startsWith('Missing supported native target evidence: '))).toBe(true)
    const commands = readFileSync(calls, 'utf8').trim().split('\n').map(line => JSON.parse(line))
    expect(commands).toHaveLength(5)
    expect(commands.every(command => command[0] === 'git')).toBe(true)
    expect(existsSync(join(root, inputs[0]))).toBe(true)
  } finally {
    rmSync(owned, { recursive: true, force: true })
    rmSync(lane, { recursive: true, force: true })
  }
})
