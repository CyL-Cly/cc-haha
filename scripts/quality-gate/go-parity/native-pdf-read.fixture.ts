import { parse } from 'acorn'
import * as path from 'node:path'
import { createHash } from 'node:crypto'
import * as nativeFiles from 'node:fs/promises'
import { execFile } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { assertDisposableFixtureHome } from './fixture-guard'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const home = assertDisposableFixtureHome(input.home, 'Actual PDF utility and Read producer')
if (process.env.CC_HAHA_PDF_READ_FIXTURE !== '1') throw new Error('Unowned PDF Read fixture')
const physical = input.physical === true
if (physical && (Bun.version !== '1.3.14' || process.env.CC_HAHA_PDF_PHYSICAL_FIXTURE !== '1' || !path.isAbsolute(input.popplerBin) || process.env.PATH.split(path.delimiter)[0] !== input.popplerBin)) throw new Error('Physical PDF primitives require the pinned declared private dependency')
if (physical) {
  const directory = await nativeFiles.realpath(input.popplerBin)
  const artifacts = await nativeFiles.realpath(fileURLToPath(new URL('../../../artifacts', import.meta.url)))
  if (!directory.startsWith(artifacts + path.sep) && !['/private/tmp/', '/tmp/', '/private/var/folders/', '/var/folders/'].some(root => directory.startsWith(root))) throw new Error('Physical PDF dependency must stay in private temporary or current project artifacts storage')
  for (const name of ['pdfinfo', 'pdftoppm']) {
    const executable = await nativeFiles.stat(path.join(directory, name))
    if (!executable.isFile() || !(executable.mode & 0o111)) throw new Error('Actual executable PDF dependency is required')
  }
}
const nativeResize = physical ? (await import('../../../src/utils/imageResizer')).maybeResizeAndDownsampleImageBuffer : null

// Execute the unchanged Source function bodies. The normal corpus supplies
// controlled filesystem/process/codec primitives. The explicitly owned physical
// branch instead uses confined private files, the declared Poppler dependency
// and the original image codec; app-state and telemetry remain fixture inputs.
async function source(file: string) {
  const text = await Bun.file(new URL(file, import.meta.url)).text()
  const code = new Bun.Transpiler({ loader: 'ts' }).transformSync(text)
  const ast = parse(code, { ecmaVersion: 'latest', sourceType: 'module' }) as any
  const nodes = ast.body.map((node: any) => node.type === 'ExportNamedDeclaration' ? node.declaration : node)
  const declaration = (name: string) => {
    const node = nodes.find((node: any) => node?.type === 'FunctionDeclaration' && node.id?.name === name)
    if (!node) throw new Error(`Missing actual ${file} declaration ${name}`)
    return code.slice(node.start, node.end)
  }
  return { text, code, nodes, declaration }
}

const pdfSource = await source('../../../src/utils/pdf.ts')
const utilsSource = await source('../../../src/utils/pdfUtils.ts')
const formatSource = await source('../../../src/utils/format.ts')
const errorSource = await source('../../../src/utils/errors.ts')
const constantsSource = await source('../../../src/constants/apiLimits.ts')
const constants: Record<string, number> = {}
for (const name of ['PDF_TARGET_RAW_SIZE', 'PDF_MAX_EXTRACT_SIZE', 'PDF_EXTRACT_SIZE_THRESHOLD', 'PDF_MAX_PAGES_PER_READ', 'PDF_AT_MENTION_INLINE_THRESHOLD']) {
  const node = constantsSource.nodes.find((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((declaration: any) => declaration.id.name === name))
  if (!node) throw new Error(`Missing actual PDF constant ${name}`)
  constants[name] = new Function(`${constantsSource.code.slice(node.start, node.end)};return ${name}`)()
}
const formatFileSize = new Function(`${formatSource.declaration('formatFileSize')};return formatFileSize`)()
const errorMessage = new Function(`${errorSource.declaration('errorMessage')};return errorMessage`)()
let currentModel = 'deepseek-flash'
const utils = new Function('getMainLoopModel', `const DOCUMENT_EXTENSIONS=new Set(['pdf']);${['parsePDFPageRange', 'isPDFSupported', 'isPDFExtension'].map(utilsSource.declaration).join('\n')};return {parsePDFPageRange,isPDFSupported,isPDFExtension}`)(() => currentModel)

const toolSource = await source('../../../src/tools/FileReadTool/FileReadTool.ts')
const toolNode = toolSource.nodes.find((node: any) => node?.type === 'VariableDeclaration' && node.declarations.some((declaration: any) => declaration.id.name === 'FileReadTool'))
const toolObject = toolNode?.declarations.find((declaration: any) => declaration.id.name === 'FileReadTool')?.init?.arguments?.[0]
const method = (name: string) => {
  const property = toolObject?.properties.find((property: any) => property.key?.name === name)
  if (!property || !property.method) throw new Error(`Missing actual FileReadTool ${name} method`)
  return `({${toolSource.code.slice(property.start, property.end)}}).${name}`
}

function rangeProjection(value: any) {
  return value === null ? null : { firstPage: String(value.firstPage), lastPage: String(value.lastPage) }
}
const rangeRows: any[] = (input.ranges ?? []).map((pages: string) => ({ range: rangeProjection(utils.parsePDFPageRange(pages)) }))
let denied = false
const validatePDFInput = new Function('deps', `with(deps){return ${method('validateInput')}}`)({
  path, ...utils, ...constants, resolveRelocatedAttachmentPath: (value: string) => value,
  expandPath: (value: string) => value, matchingRuleForInput: () => denied ? { ruleValue: 'Read' } : null,
  hasBinaryExtension: () => false, IMAGE_EXTENSIONS: new Set(), isBlockedDevicePath: () => false,
})
for (let i = 0; i < rangeRows.length; i++) rangeRows[i].validation = await validatePDFInput({ file_path: path.join(home, 'report.pdf'), pages: input.ranges[i] }, { getAppState: () => ({ toolPermissionContext: {} }) })
const validations = []
for (const row of input.validations ?? []) {
  denied = row.denied ?? false
  validations.push(await validatePDFInput({ file_path: row.path ?? path.join(home, row.file ?? 'report.pdf'), ...(row.pages !== undefined && { pages: row.pages }) }, { getAppState: () => ({ toolPermissionContext: {} }) }))
}

const rows = []
const physicalCache = new Map()
for (const row of input.rows ?? []) {
  const filePath = path.join(home, row.file ?? 'report.pdf')
  const resultsDirectory = path.join(home, 'results')
  const calls: any[] = []
  let statIndex = 0
  let runIndex = 0
  const abortController = new AbortController()
  let selectedResultsDirectory = resultsDirectory
  const owned = (value: string) => {
    if (value !== filePath && !value.startsWith(resultsDirectory + path.sep)) throw new Error('Unowned PDF filesystem path')
  }
  const failure = (message: string | undefined) => { if (message !== undefined) throw new Error(message) }
  const stat = async (value: string) => {
    owned(value)
    calls.push({ type: 'stat', path: value })
    failure(row.statError)
    if (physical) return nativeFiles.stat(value)
    const next = row.stats?.[Math.min(statIndex++, row.stats.length - 1)]
    return { size: next?.size ?? row.size ?? 8, mtimeMs: next?.mtime ?? row.mtime ?? 1234.75 }
  }
  const readFile = async (value: string) => {
    owned(value)
    calls.push({ type: 'read', path: value })
    failure(row.readError)
    if (physical) return nativeFiles.readFile(value)
    if (value === filePath) return Buffer.from(row.bytes ?? 'JVBERi0xLjQ=', 'base64')
    const name = path.basename(value)
    return Buffer.from(row.images?.[name] ?? '/9j/AA==', 'base64')
  }
  const readdir = async (value: string) => {
    owned(value)
    calls.push({ type: 'readdir', path: value })
    failure(row.directoryError)
    if (physical) return nativeFiles.readdir(value)
    return row.entries ?? ['page-01.jpg']
  }
  const mkdir = async (value: string, options: any) => {
    owned(value)
    if (options?.recursive !== true) throw new Error('Source mkdir must be recursive')
    calls.push({ type: 'mkdir', path: value })
    failure(row.mkdirError)
    if (physical) return nativeFiles.mkdir(value, options)
  }
  const execFileNoThrow = async (executable: string, args: string[], options: any) => {
    if (executable !== 'pdfinfo' && executable !== 'pdftoppm') throw new Error('Unowned PDF process')
    if (options.useCwd !== false) throw new Error('Source PDF process unexpectedly uses current working directory')
    calls.push({ type: 'run', executable, args, timeout: options.timeout })
    if (physical) {
      if (args.some(value => value.includes(path.sep) && value !== filePath && !value.startsWith(resultsDirectory + path.sep))) throw new Error('Physical PDF subprocess received an unowned path')
      const started = performance.now()
      return await new Promise(resolve => execFile(path.join(input.popplerBin, executable), args, { cwd: home, env: { ...process.env }, timeout: options.timeout, maxBuffer: 100000000 }, (error: any, stdout, stderr) => {
        calls.push({ type: 'physical-process', executable, elapsedMs: performance.now() - started, code: error ? typeof error.code === 'number' ? error.code : 1 : 0, stderr })
        resolve({ code: error ? typeof error.code === 'number' ? error.code : 1 : 0, stdout, stderr })
      }))
    }
    const result = row.processes?.[runIndex++]
    if (!result) throw new Error('Unexpected additional PDF subprocess')
    if (row.abortAfterProcess === runIndex) abortController.abort()
    failure(result.error)
    return { code: result.code ?? 0, stdout: result.stdout ?? '', stderr: result.stderr ?? '' }
  }
  const utilityBodies = ['readPDF', 'getPDFPageCount', 'resetPdftoppmCache', 'isPdftoppmAvailable', 'extractPDFPages'].map(pdfSource.declaration).join('\n')
  const utilities = new Function('deps', `with(deps){let pdftoppmAvailable;${utilityBodies};return {readPDF,getPDFPageCount,resetPdftoppmCache,isPdftoppmAvailable,extractPDFPages}}`)({
    getFsImplementation: () => ({ stat }), readFile, readdir, mkdir, join: path.join,
    execFileNoThrow, randomUUID: () => 'fixture', getToolResultsDir: () => selectedResultsDirectory,
    formatFileSize, errorMessage, ...constants,
  })
  const cache = physical ? physicalCache : new Map()
  const dependencies = {
    ...utilities, ...utils, ...constants, path, IMAGE_EXTENSIONS: new Set(),
    getFsImplementation: () => ({ stat }), readdir, readFileAsync: readFile, formatFileSize,
    maybeResizeAndDownsampleImageBuffer: async (buffer: Buffer, originalSize: number, extension: string) => {
      calls.push({ type: 'image', bytes: buffer.toString('base64'), originalSize, extension })
      failure(row.imageError)
      if (nativeResize) return nativeResize(buffer, originalSize, extension)
      return { buffer, mediaType: 'jpeg' }
    },
    logEvent: () => {}, logFileOperation: () => {},
    createUserMessage: (value: any) => ({ message: { content: value.content }, isMeta: value.isMeta }),
    getDefaultFileReadingLimits: () => ({ maxSizeBytes: 1, maxTokens: 1 }),
    getFeatureValue_CACHED_MAY_BE_STALE: () => false,
    resolveRelocatedAttachmentPath: (value: string) => value, expandPath: (value: string) => value,
    getCwd: () => home, isEnvTruthy: () => true, getErrnoCode: () => undefined,
  }
  const inner = new Function('deps', `with(deps){${toolSource.declaration('callInner')};return callInner}`)(dependencies)
  const call = new Function('deps', `with(deps){return ${method('call')}}`)({ ...dependencies, callInner: inner })
  currentModel = row.model ?? 'deepseek-flash'
  let output: any
  try {
    switch (row.action) {
      case 'raw': output = await utilities.readPDF(filePath); break
      case 'count': output = { count: String(await utilities.getPDFPageCount(filePath)) }; break
      case 'probe': output = { available: [] }; for (let i = 0; i < (row.repetitions ?? 1); i++) output.available.push(await utilities.isPdftoppmAvailable()); break
      case 'extract': output = await utilities.extractPDFPages(filePath, row.options); break
      case 'promote': {
        output = { data: [] }
        for (const directory of ['original', 'promoted', 'original']) {
          selectedResultsDirectory = path.join(resultsDirectory, directory)
          output.data.push(await utilities.extractPDFPages(filePath))
        }
        break
      }
      case 'read': {
        const result = await call({ file_path: filePath, pages: row.pages }, { readFileState: cache, abortController, getAppState: () => ({}) })
        output = { data: result.data, blocks: result.newMessages?.[0]?.message.content ?? null,
          cache: [...cache.entries()].map(([path, state]) => ({ path, ...state })) }
        break
      }
      default: throw new Error('Unknown PDF fixture action')
    }
  } catch (error) { output = { thrown: errorMessage(error) } }
  if (row.abortAfterProcess) output.aborted = abortController.signal.aborted
  rows.push({ output, calls })
}
process.stdout.write(JSON.stringify({ ranges: rangeRows, validations, rows, authority: {
  pdf: createHash('sha256').update(pdfSource.text).digest('hex'),
  utils: createHash('sha256').update(utilsSource.text).digest('hex'),
  tool: createHash('sha256').update(toolSource.text).digest('hex'),
}, scope: physical
  ? 'actual utility functions and FileReadTool.call/validateInput/callInner with confined actual FS/Poppler/original imageResizer; fixture app-state and telemetry'
  : 'actual utility functions and actual FileReadTool.call/validateInput/callInner, controlled native FS/process/image-codec primitives' }))
