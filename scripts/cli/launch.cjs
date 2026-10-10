#!/usr/bin/env node
'use strict'

// npm requires a JavaScript entry on Windows. This file only locates the
// native executable and forwards its process I/O; CLI business belongs to Go.
const { statSync } = require('node:fs')
const { isAbsolute, join, resolve } = require('node:path')
const { spawn } = require('node:child_process')
const { constants } = require('node:os')

function nativeName(platform = process.platform, arch = process.arch) {
  const os = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[platform]
  const cpu = { x64: 'amd64', arm64: 'arm64' }[arch]
  if (!os || !cpu) throw new Error(`Unsupported native CLI platform: ${platform}/${arch}`)
  return `claude-haha-${os}-${cpu}${platform === 'win32' ? '.exe' : ''}`
}

function executable(root, env = process.env, platform = process.platform, arch = process.arch) {
  const override = env.CC_HAHA_GO_BINARY
  if (override && !isAbsolute(override)) throw new Error('CC_HAHA_GO_BINARY must be an absolute path')
  const binary = override || join(root, 'bin', 'native', nativeName(platform, arch))
  let stat
  try { stat = statSync(binary) } catch {}
  if (!stat || !stat.isFile() || (platform !== 'win32' && !(stat.mode & 0o111))) {
    throw new Error(`Go executable is missing or not executable: ${binary}\nBuild it with bun run build:cli, or install the native executable with bun run install:cli -- --prefix /absolute/path.`)
  }
  return binary
}

function launch() {
  let binary
  try { binary = executable(resolve(__dirname, '../..')) } catch (error) {
    console.error(`claude-haha: ${error.message}`)
    process.exitCode = 1
    return
  }
  const child = spawn(binary, process.argv.slice(2), {
    cwd: process.cwd(),
    env: { ...process.env, CALLER_DIR: process.env.CALLER_DIR || process.cwd() },
    stdio: 'inherit',
    windowsHide: false,
  })
  const signals = ['SIGINT', 'SIGTERM']
  const handlers = signals.map(signal => () => {
    try { child.kill(signal) } catch {}
  })
  signals.forEach((signal, index) => process.on(signal, handlers[index]))
  const cleanup = () => signals.forEach((signal, index) => process.off(signal, handlers[index]))
  child.once('error', error => {
    cleanup()
    console.error(`claude-haha: cannot start Go executable: ${error.message}`)
    process.exitCode = 1
  })
  child.once('exit', (code, signal) => {
    cleanup()
    process.exitCode = code === null ? 128 + (constants.signals[signal] || 1) : code
  })
}

module.exports = { nativeName, executable }
if (require.main === module) launch()
