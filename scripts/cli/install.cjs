#!/usr/bin/env node
'use strict'

const { copyFileSync, chmodSync, constants, mkdirSync, renameSync, rmSync, statSync } = require('node:fs')
const { isAbsolute, join, resolve } = require('node:path')
const { nativeName } = require('./launch.cjs')

function installCLI({ root = resolve(__dirname, '../..'), prefix, source, platform = process.platform, arch = process.arch }) {
  if (!prefix || !isAbsolute(prefix)) throw new Error('CLI installation requires an explicit absolute --prefix')
  const binary = source || join(root, 'bin', 'native', nativeName(platform, arch))
  if (!isAbsolute(binary) || !statSync(binary).isFile()) throw new Error('CLI installation requires a built native executable')
  const directory = join(prefix, 'bin')
  const destination = join(directory, `claude-haha${platform === 'win32' ? '.exe' : ''}`)
  mkdirSync(directory, { recursive: true })
  const temporary = `${destination}.install-${process.pid}`
  try {
    copyFileSync(binary, temporary, constants.COPYFILE_EXCL)
    chmodSync(temporary, 0o755)
    renameSync(temporary, destination)
  } finally {
    rmSync(temporary, { force: true })
  }
  return destination
}

module.exports = { installCLI }
if (require.main === module) {
  try {
    const args = process.argv.slice(2)
    if (args[0] === '--') args.shift()
    let prefix
    for (let index = 0; index < args.length; index += 2) {
      if (args[index] !== '--prefix' || !args[index + 1]) throw new Error(`Invalid CLI install argument: ${args[index]}`)
      prefix = args[index + 1]
    }
    console.log(`Installed native Go CLI: ${installCLI({ prefix })}`)
  } catch (error) {
    console.error(`claude-haha: ${error.message}`)
    process.exitCode = 1
  }
}
