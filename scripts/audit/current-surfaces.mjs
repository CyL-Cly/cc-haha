// Read-only parser. It cannot start the app, use a model, or declare acceptance.
import { createRequire } from 'node:module'
import { existsSync, readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve, relative, extname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'

export const FROZEN_MAIN = '6d21316ec4635f29ecbaa43e1a5574bfd31d7155'
const repository = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const ts = createRequire(join(repository, 'desktop/package.json'))('typescript')
const sha256 = value => createHash('sha256').update(value).digest('hex')
const parse = (file, code) => ts.createSourceFile(file, code, ts.ScriptTarget.Latest, true, file.endsWith('x') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
const walk = (node, fn) => { fn(node); ts.forEachChild(node, child => walk(child, fn)) }
const sourceLine = (sf, node) => sf.getLineAndCharacterOfPosition(node.getStart(sf)).line + 1
const productionTS = file => /\.(?:ts|tsx|mjs|cjs)$/.test(file) && !/(?:^|\/)(?:__tests__|__fixtures__|testdata)\//.test(file) && !/\.(?:test|spec|fixture)\./.test(file) && !file.endsWith('.d.ts')

export function symbolicPath(node, sf, depth = 0, substitutions = new Map()) {
  if (!node || depth > 8) return '[unresolved]'
  if (ts.isStringLiteralLike(node)) return node.text
  if (ts.isTemplateExpression(node)) return node.head.text + node.templateSpans.map(span => `${symbolicPath(span.expression, sf, depth + 1, substitutions)}${span.literal.text}`).join('')
  if (ts.isParenthesizedExpression(node)) return symbolicPath(node.expression, sf, depth + 1, substitutions)
  if (ts.isConditionalExpression(node)) return `{${symbolicPath(node.whenTrue, sf, depth + 1, substitutions)}|${symbolicPath(node.whenFalse, sf, depth + 1, substitutions)}}`
  if (ts.isBinaryExpression(node) && node.operatorToken.kind === ts.SyntaxKind.PlusToken) return symbolicPath(node.left, sf, depth + 1, substitutions) + symbolicPath(node.right, sf, depth + 1, substitutions)
  if (ts.isCallExpression(node) && ['getApiUrl', 'new URL'].includes(node.expression.getText(sf))) return symbolicPath(node.arguments[0], sf, depth + 1, substitutions)
  if (ts.isCallExpression(node) && node.expression.getText(sf) === 'buildWorkspacePath') return `/api/sessions/[sessionId]/workspace/${symbolicPath(node.arguments[1], sf, depth + 1)}[query]`
  if (ts.isIdentifier(node)) {
    if (substitutions.has(node.text)) return substitutions.get(node.text)
    let enclosing = node.parent
    while (enclosing) {
      let initializer
      for (const statement of enclosing.statements ?? []) walk(statement, child => {
        if (ts.isVariableDeclaration(child) && child.name.getText(sf) === node.text) initializer = child.initializer
      })
      if (initializer && initializer !== node) return symbolicPath(initializer, sf, depth + 1, substitutions)
      enclosing = enclosing.parent
    }
  }
  if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
    const declaration = sf.statements.find(statement => ts.isFunctionDeclaration(statement) && statement.name?.text === node.expression.text)
    const returned = declaration?.body?.statements.find(statement => ts.isReturnStatement(statement))
    if (returned) {
      const next = new Map(substitutions)
      for (const [index, parameter] of declaration.parameters.entries()) next.set(parameter.name.getText(sf), symbolicPath(node.arguments[index] ?? parameter.initializer, sf, depth + 1, substitutions))
      return symbolicPath(returned.expression, sf, depth + 1, next)
    }
  }
  return `[${node.getText(sf)}]`
}

function enclosingOperation(node, sf) {
  let current = node
  let member = null
  while (current.parent) {
    if ((ts.isPropertyAssignment(current) || ts.isMethodDeclaration(current)) && !member) member = current.name.getText(sf)
    if (ts.isVariableDeclaration(current) && ts.isIdentifier(current.name)) return member ? `${current.name.text}.${member}` : current.name.text
    if (ts.isFunctionDeclaration(current) && current.name) return current.name.text
    current = current.parent
  }
  return null
}

function methodFromOptions(node, sf) {
  if (node && ts.isObjectLiteralExpression(node)) {
    const method = node.properties.find(p => ts.isPropertyAssignment(p) && p.name.getText(sf) === 'method')
    if (method) return symbolicPath(method.initializer, sf)
  }
  return 'GET'
}

export function inspectTS(file, code) {
  const sf = parse(file, code)
  const http = [], ipc = [], socket = [], imports = [], selectors = [], callExpressions = [], constants = [], specialURLs = []
  walk(sf, node => {
    const loc = { file, line: sourceLine(sf, node) }
    if (ts.isImportDeclaration(node) && ts.isStringLiteral(node.moduleSpecifier)) {
      imports.push({ ...loc, specifier: node.moduleSpecifier.text, clause: node.importClause?.getText(sf) ?? '' })
    }
    if (ts.isPropertyAssignment(node) && ['tab', 'type'].includes(node.name.getText(sf)) && ts.isStringLiteralLike(node.initializer)) selectors.push({ ...loc, key: node.name.getText(sf), value: node.initializer.text })
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && /ELECTRON_(?:IPC|EVENT|INTERNAL)_CHANNELS/.test(node.name.text)) {
      let value = node.initializer
      if (value && ts.isAsExpression(value)) value = value.expression
      if (value && ts.isObjectLiteralExpression(value)) for (const property of value.properties) if (ts.isPropertyAssignment(property) && ts.isStringLiteralLike(property.initializer)) constants.push({ ...loc, key: `${node.name.text}.${property.name.getText(sf)}`, value: property.initializer.text })
    }
    if (ts.isStringLiteralLike(node) || ts.isTemplateExpression(node)) {
      const expression = node.getText(sf)
      if (/\/(?:preview-fs|local-file|sdk|ws|proxy)\//.test(expression)) specialURLs.push({ ...loc, expression, path: symbolicPath(node, sf) })
    }
    if (!ts.isCallExpression(node)) return
    const expression = node.expression.getText(sf)
    const operation = enclosingOperation(node, sf)
    callExpressions.push({ ...loc, expression, operation })
    const apiMethod = /^api\.(get|post|put|patch|delete|response)$/.exec(expression)?.[1]
    if (apiMethod || expression === 'fetch' || expression.endsWith('.fetch')) {
      const path = symbolicPath(node.arguments[0], sf)
      if (apiMethod || /^\/?(?:api|proxy|local-file|preview-fs)\//.test(path)) {
        const method = apiMethod && apiMethod !== 'response' ? apiMethod.toUpperCase() : methodFromOptions(node.arguments[1], sf)
        const family = /^\/api\/([^/\[?]+)/.exec(path)?.[1] ?? (/^\/([^/]+)/.exec(path)?.[1] ?? null)
        http.push({ ...loc, operation, method, path, family, expression: node.arguments[0]?.getText(sf) ?? '', pathResolution: path === '[unresolved]' || !path.startsWith('/') ? 'unresolved' : 'symbolic-path' })
      }
    }
    if (/^(?:invoke|subscribe|registerHandler)$/.test(expression) || /\.(?:invoke|subscribe|handle|on|send)$/.test(expression)) {
      const channel = node.arguments[0]?.getText(sf) ?? ''
      if (/ELECTRON_(?:IPC|EVENT|INTERNAL)_CHANNELS\./.test(channel)) ipc.push({ ...loc, operation, call: expression, channelKey: channel, handler: expression === 'registerHandler' || expression.endsWith('.handle') || expression === 'ipcMain.on' ? node.arguments[1]?.getText(sf) ?? '' : null })
    }
    if (/(?:^|\.)(?:send|sendMessage|sendControl|sendControlRequest)$/.test(expression)) {
      const payload = node.arguments.find(a => ts.isObjectLiteralExpression(a))
      if (payload) {
        const type = payload.properties.find(p => ts.isPropertyAssignment(p) && p.name.getText(sf) === 'type')
        if (type) socket.push({ ...loc, operation, call: expression, type: symbolicPath(type.initializer, sf) })
      }
    }
  })
  return { http, ipc, socket, imports, selectors, callExpressions, constants, specialURLs }
}

function filesUnder(root) {
  const out = []
  const recurse = directory => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const file = join(directory, entry.name)
      if (entry.isDirectory()) {
        if (!['node_modules', 'dist', 'electron-dist', 'build-artifacts', 'target', '.git', '.build'].includes(entry.name)) recurse(file)
      } else out.push(relative(root, file))
    }
  }
  recurse(root)
  return out.sort()
}

function moduleTarget(file, specifier, available) {
  const stem = specifier.startsWith('@/') ? 'desktop/src/' + specifier.slice(2) : specifier.startsWith('.') ? relative(repository, resolve(repository, dirname(file), specifier)) : null
  if (!stem) return null
  const withoutJS = stem.replace(/\.js$/, '')
  return [stem, ...['.ts', '.tsx', '.mjs', '.cjs'].map(s => withoutJS + s), withoutJS + '/index.ts', withoutJS + '/index.tsx'].find(s => available.has(s)) ?? null
}

function graphOf(modules) {
  const available = new Set(modules.keys())
  const outgoing = new Map(), incoming = new Map()
  for (const [file, module] of modules) {
    const targets = module.analysis.imports.map(i => moduleTarget(file, i.specifier, available)).filter(Boolean)
    outgoing.set(file, targets)
    for (const target of targets) {
      if (!incoming.has(target)) incoming.set(target, [])
      incoming.get(target).push(file)
    }
  }
  return { outgoing, incoming }
}

function consumerChains(file, graph) {
  const seen = new Set([file]), queue = [[file]], result = []
  while (queue.length) {
    const chain = queue.shift()
    for (const caller of graph.incoming.get(chain[0]) ?? []) {
      if (seen.has(caller)) continue
      seen.add(caller)
      const next = [caller, ...chain]
      if (/^desktop\/src\/(?:pages|components|features|layouts)\//.test(caller) && caller.endsWith('.tsx')) result.push(next)
      queue.push(next)
    }
  }
  return result.sort((a, b) => a[0].localeCompare(b[0]))
}

function readFrozen(files) {
  const modules = new Map()
  for (const file of files.filter(productionTS)) {
    const bytes = execFileSync('git', ['show', `${FROZEN_MAIN}:${file}`], { cwd: repository, maxBuffer: 5_000_000 })
    modules.set(file, { hash: sha256(bytes), analysis: inspectTS(file, bytes.toString('utf8')) })
  }
  return modules
}

function currentModules(files) {
  const modules = new Map()
  for (const file of files.filter(productionTS)) {
    const bytes = readFileSync(join(repository, file))
    modules.set(file, { hash: sha256(bytes), analysis: inspectTS(file, bytes.toString('utf8')) })
  }
  return modules
}

function currentReceipts() {
  // Only safe summary keys from top-level scoped receipts. Never import raw
  // requests, transcripts, terminal/UI snapshots, nested logs or credentials.
  const root = join(repository, 'artifacts/go-main-sync')
  const result = []
  if (!existsSync(root)) return result
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (!entry.isFile() || !entry.name.endsWith('.json') || /manifest|corpus|request|response|trace|snapshot|capture|private|tool|inventory|gap|input/i.test(entry.name)) continue
    const file = relative(repository, join(root, entry.name))
    const bytes = readFileSync(join(repository, file))
    if (bytes.length > 1_000_000) continue
    try {
      const data = JSON.parse(bytes.toString('utf8'))
      const summary = {}
      for (const key of ['status', 'scope', 'wholeComputerUseAcceptance', 'migrationComplete', 'acceptance', 'candidate', 'binarySHA256', 'binarySha256', 'candidateSHA256', 'sourceSHA', 'sourceSha', 'createdAt', 'date', 'timestamp']) {
        const value = data[key]
        if (['string', 'boolean', 'number'].includes(typeof value)) summary[key] = value
      }
      result.push({ file, sha256: sha256(bytes), summary, evidenceClass: /live/.test(entry.name) ? 'scoped-live-receipt-unmatched-to-final-candidate' : 'scoped-receipt-unmatched-to-final-candidate' })
    } catch { /* Reports in progress are not evidence. */ }
  }
  return result.sort((a, b) => a.file.localeCompare(b.file))
}

export function buildInventory(goAST) {
  const allDesktop = filesUnder(join(repository, 'desktop')).map(file => 'desktop/' + file).filter(file => /^desktop\/(?:src|electron)\//.test(file))
  const frozenPaths = execFileSync('git', ['ls-tree', '-r', '--name-only', FROZEN_MAIN, 'desktop/src', 'desktop/electron'], { cwd: repository, encoding: 'utf8' }).trim().split('\n')
  const current = currentModules(allDesktop), frozen = readFrozen(frozenPaths)
  const graph = graphOf(current), frozenGraph = graphOf(frozen)
  const router = goAST.functions.find(f => f.file === 'backend/internal/server/server.go' && f.function === 'serveAPI')
  const routes = router.selectors.filter(s => s.expression === 'parts[1]').flatMap(s => s.values.map(family => ({ family, location: { file: s.file, line: s.line }, handlerCalls: s.calls.filter(c => /HTTP|\.Handle(?:HTTP)?$/.test(c)), registrationStatus: 'dispatcher-present-behavior-not-proven' })))
  const binding = goAST.bindings.find(b => b.file === 'backend/internal/application/server.go' && b.type === 'server.Options' && b.function === 'NewDesktopServer')
  const bindings = binding ? Object.entries(binding.fields).map(([field, expression]) => ({ field, expression, file: binding.file, line: binding.line, status: 'production-construction-binding-observed-not-runtime-proof' })) : []
  const routeMap = new Map(routes.map(route => [route.family, route]))
  const decorate = (file, call, modules, graph, source) => {
    const direct = graph.incoming.get(file) ?? []
    const exact = direct.flatMap(caller => modules.get(caller).analysis.callExpressions.filter(c => call.operation && c.expression === call.operation).map(c => ({ file: caller, line: c.line, expression: c.expression })))
    const route = routeMap.get(call.family)
    const handlers = route?.handlerCalls.map(c => c.replace(/^server\./, '')).flatMap(name => goAST.functions.filter(f => f.file.startsWith('backend/internal/server/') && f.function === name).map(f => ({ file: f.file, line: f.line, function: f.function }))) ?? []
    return { ...call, source, directImporterFiles: direct, exactSymbolConsumers: exact, visibleImportChains: consumerChains(file, graph), goRoute: route ?? null, goHandlerBoundaries: source === 'WORK' ? handlers : [], status: source === 'MAIN' ? 'reference-contract' : call.pathResolution === 'unresolved' ? 'requires-path-resolution' : route ? 'route-and-construction-observed-acceptance-open' : call.family === 'public-access' ? 'special-control-transport-review' : 'requires-special-route-review', acceptance: 'not-proven-by-static-inventory' }
  }
  const frontendHttpCalls = [...current].flatMap(([file, m]) => m.analysis.http.map(call => decorate(file, call, current, graph, 'WORK')))
  const sourceHttpCalls = [...frozen].flatMap(([file, m]) => m.analysis.http.map(call => decorate(file, call, frozen, frozenGraph, 'MAIN')))
  const native = [...current].flatMap(([file, m]) => m.analysis.ipc.map(call => ({ ...call, visibleImportChains: consumerChains(file, graph), acceptance: 'native-handler-observation-is-not-behavior-proof' })))
  const nativeChannels = current.get('desktop/electron/ipc/channels.ts').analysis.constants.map(channel => ({ ...channel, callers: native.filter(c => c.channelKey === channel.key && c.handler === null).map(({ file, line, call }) => ({ file, line, call })), handlers: native.filter(c => c.channelKey === channel.key && c.handler !== null).map(({ file, line, call, handler }) => ({ file, line, call, handler })), status: channel.key.startsWith('ELECTRON_EVENT') ? 'event-producer-consumer-review' : native.some(c => c.channelKey === channel.key && c.handler !== null) ? 'native-handler-present-acceptance-open' : 'requires-native-handler-review' }))
  const socket = [...current].flatMap(([, m]) => m.analysis.socket)
  const settings = current.get('desktop/src/pages/Settings.tsx').analysis.selectors.filter(s => s.key === 'tab')
  const mainSettings = frozen.get('desktop/src/pages/Settings.tsx').analysis.selectors.filter(s => s.key === 'tab')
  const rootOptions = JSON.parse(readFileSync(join(repository, 'backend/internal/cliargs/spec.json'), 'utf8')).profiles.normal.options
  const commandCode = readFileSync(join(repository, 'backend/internal/application/command.go'), 'utf8')
  const accepted = new Set([...commandCode.matchAll(/"(--[^" ]+|-[a-zA-Z])"\s*:\s*true/g)].map(m => m[1]))
  // Each array shown here feeds the accepted map in the actual parser. Do not
  // count unrelated strings/options downstream as admission.
  for (const block of commandCode.matchAll(/for _, key := range \[\]string\{([^}]+)\}\s*\{\s*accepted\[key\] = true/g)) for (const value of block[1].matchAll(/"([^"]+)"/g)) accepted.add(value[1])
  for (const block of commandCode.matchAll(/((?:accepted\["[^"]+"\],?\s*)+)\s*=\s*(?:true,?\s*)+/g)) for (const value of block[1].matchAll(/accepted\["([^"]+)"\]/g)) accepted.add(value[1])
  const cliOptions = rootOptions.map(option => ({ flags: option.flags, attribute: option.attribute, hidden: option.hidden, admission: [option.long, option.short].filter(Boolean).some(flag => accepted.has(flag)) ? 'root-parser-admitted-consumer-acceptance-open' : option.long === '--version' ? 'early-version-parser' : option.long === '--tmux' ? 'earlier-tmux-worktree-fast-path-other-profile-review' : 'root-parser-capability-error', condition: option.long === '--chrome' ? 'false value/no-chrome admitted; true rejected' : null }))
  const guards = goAST.functions.flatMap(fn => fn.guards)
  const hashes = [...current].map(([file, m]) => ({ file, sha256: m.hash, frozenMAINHash: frozen.get(file)?.hash ?? null, sameAsMAIN: frozen.get(file)?.hash === m.hash }))
  for (const file of goAST.files) hashes.push({ file, sha256: goAST.fileHashes?.[file] ?? sha256(readFileSync(join(repository, file))) })
  const plan = readFileSync(join(repository, '.agents/plans/go-main-feature-sync-plan-20261012.md'), 'utf8')
  const requirements = [...plan.matchAll(/^\| ((?:D|R|Q)\d\d)(?: [^|]+)? \|(.+)$/gm)].map(m => ({ id: m[1], frozenRequirementRow: m[0], implementationClosed: false, acceptanceClosed: false, reason: 'Goal-wide completion requires current matched candidate evidence, not this static scan' }))
  const historical = ['go-desktop-full-api-inventory.json', 'go-cli-tui-work-inventory.json', 'go-server-boundary-work-inventory.json'].map(name => ({ file: `.agents/plans/${name}`, sha256: sha256(readFileSync(join(repository, '.agents/plans', name))), treatment: 'historical-preserved-not-current-status' }))
  return { schemaVersion: 1, generatedAt: new Date().toISOString(), scope: 'Current static producer/consumer inventory; all acceptance remains separately proven', sourceIdentity: { frozenMAIN: FROZEN_MAIN, WORK_HEAD: execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repository, encoding: 'utf8' }).trim(), currentInputsAggregate: sha256(JSON.stringify(hashes)), historical }, methodology: { parsers: ['TypeScript compiler AST', 'Go standard go/parser AST'], doesExecuteProduct: false, doesUseModel: false, symbolicDynamicPaths: true, visibleImportChainsArePotentialReachability: true, noAutomaticCompletionFromRouteOrTests: true, directImportGraphDoesNotProveAllDynamicImports: true }, counts: { currentProductionDesktopModules: current.size, frozenMAINProductionDesktopModules: frozen.size, currentFrontendHttpCalls: frontendHttpCalls.length, frozenMAINFrontendHttpCalls: sourceHttpCalls.length, registeredAPIFamilies: routes.length, unresolvedCurrentHTTPPaths: frontendHttpCalls.filter(c => c.pathResolution === 'unresolved').length, nativeIPCAndEventCalls: native.length, nativeChannels: nativeChannels.length, socketSends: socket.length, currentGoGuardSites: guards.length, rootOptionsCapabilityRejected: cliOptions.filter(o => o.admission === 'root-parser-capability-error').length, planRequirements: requirements.length }, frontendHttpCalls, sourceHttpCalls, routes, productionServerBindings: bindings, goServerFunctions: goAST.functions.filter(f => f.file.startsWith('backend/internal/server/')), nativeIPCAndEventCalls: native, nativeChannels, specialURLProducers: [...current].flatMap(([file, m]) => m.analysis.specialURLs.map(call => ({ ...call, visibleImportChains: consumerChains(file, graph), acceptance: 'special-file-transport-requires-actual-bytes-and-boundaries' }))), socketSends: socket, currentSettingsEntries: settings, frozenMAINSettingsEntries: mainSettings, cliOptions, goGuardSites: guards, requirements, scopedReceipts: currentReceipts(), inputHashes: hashes, sourceModuleHashes: [...frozen].map(([file, m]) => ({ file, sha256: m.hash })), completion: { achieved: false, reason: 'Current inventory is a static audit and scoped evidence index, not complete UI/runtime/benchmark acceptance' } }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const astPath = process.argv[2]
  if (!astPath) throw new Error('Usage: bun scripts/audit/current-surfaces.mjs <Go AST JSON> [output JSON]')
  const inventory = buildInventory(JSON.parse(readFileSync(astPath, 'utf8')))
  const output = resolve(process.argv[3] ?? join(repository, '.agents/plans/go-current-surface-inventory-20261013.json'))
  writeFileSync(output, JSON.stringify(inventory, null, 2) + '\n')
  process.stdout.write(JSON.stringify({ output, counts: inventory.counts, sourceIdentity: inventory.sourceIdentity }) + '\n')
}
