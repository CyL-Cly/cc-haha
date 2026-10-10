import { spyOn } from 'bun:test'
import * as fs from 'node:fs/promises'
import * as crypto from 'node:crypto'
import os from 'node:os'
import { join, relative, isAbsolute } from 'node:path'
import type { H5NetworkInterface, H5ServiceEnvironment, H5ServiceOutput, H5ServiceRequest, H5ServiceResult } from './h5-service-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as H5ServiceRequest
if (!input.homeDir || input.homeDir !== process.env.HOME || process.env.CLAUDE_CONFIG_DIR !== join(input.homeDir, '.claude')) {
  throw new Error('H5 service fixture requires explicit disposable HOME/config')
}
// Validate the sandbox before loading modules that own persistence or OAuth.
const { H5AccessService, classifyH5PublicBaseUrl, validateH5PublicBaseUrl, collectLocalIPv4Hosts, findPrivateLanAddress, resolveEffectiveH5PublicBaseUrl } = await import('../../../src/server/services/h5AccessService')
const { ProviderService } = await import('../../../src/server/services/providerService')
const { isBrowserSafePort } = await import('../../../src/server/services/browserSafePort')
const migrations = await import('../../../src/server/services/persistentStorageMigrations')
const { ApiError } = await import('../../../src/server/middleware/errorHandler')
const output: H5ServiceOutput = { results: [], effects: [], randomCalls: [], interfaceCalls: 0, portCalls: 0, warningCount: 0, snapshots: [] }
let interfaces = input.interfaces
let activePort = input.activePort
let tokenGeneration = 0

function environment(values: H5ServiceEnvironment) {
  for (const key of ['CLAUDE_H5_PUBLIC_BASE_URL', 'CLAUDE_H5_AUTO_PUBLIC_URL'] as const) {
    if (!Object.hasOwn(values, key)) continue
    if (values[key] === null) delete process.env[key]
    else process.env[key] = values[key]
  }
}
environment(input.environment)

function networkInterfaces(values: H5NetworkInterface[]) {
  // Keep actual Object.values/entries enumeration, including numeric adapter
  // names and the source's strict string comparison for family === 'IPv4'.
  return Object.fromEntries(values.map(value => [value.name, value.entries])) as ReturnType<typeof os.networkInterfaces>
}
spyOn(os, 'networkInterfaces').mockImplementation(() => { output.interfaceCalls++; return networkInterfaces(interfaces) })
spyOn(ProviderService, 'getServerPort').mockImplementation(() => { output.portCalls++; return activePort })
spyOn(Date, 'now').mockReturnValue(1_700_000_000_000)
spyOn(crypto, 'randomBytes').mockImplementation(((size: number) => {
  output.randomCalls.push(size)
  if (size === 32) {
    tokenGeneration++
    return Buffer.from(Array.from({ length: size }, (_, index) => (index + tokenGeneration * 17) % 256))
  }
  return Buffer.from(Array.from({ length: size }, (_, index) => index % 3 + 1))
}) as never)
console.warn = () => { output.warningCount++ }

const originalReadFile = fs.readFile
const originalReaddir = fs.readdir
function fixtureRelative(path: string): string {
  const name = relative(input.homeDir, path).replace(/\\/g, '/')
  if (name === '..' || name.startsWith('../') || isAbsolute(name)) throw new Error(`H5 fixture path escaped HOME: ${path}`)
  return name
}
const managedPath = join(input.homeDir, '.claude', 'cc-haha', 'settings.json')
for (const [method, pathCount] of [['readFile', 1], ['writeFile', 1], ['mkdir', 1], ['rename', 2], ['copyFile', 2], ['unlink', 1], ['access', 1]] as const) {
  const original = fs[method] as (...args: unknown[]) => Promise<unknown>
  spyOn(fs, method).mockImplementation((async (...args: unknown[]) => {
    for (const path of args.slice(0, pathCount)) fixtureRelative(String(path))
    const result = await original(...args)
    if (method === 'rename' && String(args[1]) === managedPath) {
      output.effects.push({ operation: 'write', path: fixtureRelative(managedPath), value: JSON.parse(await originalReadFile(managedPath, 'utf8')) })
    }
    return result
  }) as never)
}
const originalMigration = migrations.ensurePersistentStorageUpgraded
spyOn(migrations, 'ensurePersistentStorageUpgraded').mockImplementation(async () => {
  output.effects.push({ operation: 'migration', config: fixtureRelative(process.env.CLAUDE_CONFIG_DIR!) })
  return originalMigration()
})

async function snapshot() {
  const files: H5ServiceOutput['snapshots'][number] = {}
  async function walk(directory: string): Promise<void> {
    let entries: import('node:fs').Dirent[]
    try { entries = await originalReaddir(directory, { withFileTypes: true }) } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') return
      throw error
    }
    for (const entry of entries.sort((left, right) => left.name < right.name ? -1 : left.name > right.name ? 1 : 0)) {
      const path = join(directory, entry.name)
      if (entry.isDirectory()) await walk(path)
      else {
        const raw = await originalReadFile(path, 'utf8')
        try { files[fixtureRelative(path)] = { json: JSON.parse(raw) } } catch { files[fixtureRelative(path)] = { raw } }
      }
    }
  }
  await walk(join(input.homeDir, '.claude'))
  return files
}

const service = new H5AccessService()
for (const step of input.steps) {
  if (step.environment) environment(step.environment)
  if (step.interfaces) interfaces = step.interfaces
  if (step.activePort !== undefined) activePort = step.activePort
  try {
    let value: unknown
    switch (step.operation) {
      case 'read': value = await service.getSettings(); break
      case 'update': {
        const patch = { ...step.patch }
        for (const key of step.unset ?? []) patch[key] = undefined
        value = await service.updateSettings(patch as Parameters<InstanceType<typeof H5AccessService>['updateSettings']>[0])
        break
      }
      case 'enable': value = await service.enable(); break
      case 'disable': value = await service.disable(); break
      case 'regenerate': value = await service.regenerateToken(); break
      case 'validate-token': {
        let token = step.value
        if (step.tokenFrom !== undefined) {
          const result = output.results[step.tokenFrom] as { value?: { token?: string } } | undefined
          if (!result?.value?.token) throw new Error(`Invalid tokenFrom fixture step: ${step.tokenFrom}`)
          token = result.value.token
        }
        value = await service.validateToken(token as string | null | undefined)
        break
      }
      case 'origin': value = await service.isOriginAllowed(step.value as string | null | undefined); break
      case 'diagnostics': value = await service.getDiagnostics(); break
      case 'grace': value = await service.getDisconnectGraceMs(); break
      case 'classify-url': value = classifyH5PublicBaseUrl(step.value as string); break
      case 'validate-url': value = validateH5PublicBaseUrl(step.value as string, step.hosts); break
      case 'collect-hosts': value = collectLocalIPv4Hosts(); break
      case 'find-lan': value = findPrivateLanAddress(); break
      case 'effective-url': value = resolveEffectiveH5PublicBaseUrl(step.effective!); break
      case 'browser-safe-port': value = isBrowserSafePort(step.value as number); break
      default: throw new Error(`Unknown H5 service fixture operation: ${step.operation}`)
    }
    output.results.push({ value } satisfies H5ServiceResult)
  } catch (error) {
    output.results.push({ error: { status: error instanceof ApiError ? error.statusCode : 500, code: error instanceof ApiError ? error.code ?? 'INTERNAL_ERROR' : 'INTERNAL_ERROR', message: error instanceof Error ? error.message : String(error) } })
  }
  output.snapshots.push(await snapshot())
}
process.stdout.write(`${JSON.stringify(output)}\n`)
