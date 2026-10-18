import { randomUUID } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { basename } from 'node:path'
import { matchWorkspaceShortcut } from '../../src/lib/workspace/shortcuts'
import type {
  PreviewBrowserControlsMessage,
  WorkspaceBrowserCaptureKind,
  WorkspaceBrowserEvent,
  WorkspaceBrowserFindOptions,
  WorkspaceBrowserHistoryEntry,
  WorkspaceBrowserMenuAction,
  WorkspaceBrowserMenuOptions,
} from '../../src/lib/desktopHost/types'
import { parsePreviewAgentMessage, type PreviewAgentMessage } from '../ipc/previewMessage'
import { parseHostMessage, type HostMessage } from '../../src/preview-agent/protocol'
import { WORKSPACE_BROWSER_INITIAL_SRC } from '../../src/lib/workspace/browserGuestContract'
import { isHttpUrl } from './navigationGuards'
import { normalizePreviewUrl, resolvePreviewScriptPath } from './preview'
import { normalizeZoomFactor } from './zoom'
import { WorkspaceBrowserMenuController, type WorkspaceBrowserMenuFactory } from './workspaceBrowserMenu'

export type { WorkspaceBrowserCaptureKind, WorkspaceBrowserEvent, WorkspaceBrowserFindOptions }
export { WORKSPACE_BROWSER_PARTITION } from '../../src/lib/workspace/browserGuestContract'

/** Mirrors the preview capture guard rails; a page controls these dimensions. */
const FULL_CAPTURE_MAX_EDGE = 16_384
const FULL_CAPTURE_MAX_PIXELS = 32_000_000

/**
 * A guest that is not on screen has no compositor frame, and `capturePage`
 * then waits ~30s before failing. Without a bound, a capture racing a tab
 * switch would keep the page's zoom chrome hidden for that long.
 */
const VIEWPORT_CAPTURE_TIMEOUT_MS = 5_000

/** Visit log bound. The native back/forward stack is unaffected by this cap. */
const MAX_HISTORY_ENTRIES = 200

type WorkspaceBrowserDebuggerLike = {
  isAttached(): boolean
  attach(protocolVersion?: string): void
  detach(): void
  sendCommand(method: string, commandParams?: Record<string, unknown>): Promise<unknown>
  on?(event: 'detach', listener: (event: unknown, reason: string) => void): unknown
  removeListener?(event: 'detach', listener: (event: unknown, reason: string) => void): unknown
}

export type WorkspaceBrowserDownloadItemLike = {
  getFilename(): string
  getSavePath(): string
  getReceivedBytes(): number
  getTotalBytes(): number
  getState(): string
  on(event: 'updated', handler: (event: unknown, state: string) => void): unknown
  on(event: 'done', handler: (event: unknown, state: string) => void): unknown
}

export type WorkspaceBrowserSessionLike = {
  on(
    event: 'will-download',
    handler: (
      event: unknown,
      item: WorkspaceBrowserDownloadItemLike,
      webContents: unknown,
    ) => void,
  ): unknown
}

type WorkspaceBrowserNavigationHistoryLike = {
  canGoBack(): boolean
  canGoForward(): boolean
  goBack(): void
  goForward(): void
  clear?(): void
}

export type WorkspaceBrowserWebContentsLike = {
  loadURL(url: string): Promise<unknown>
  getURL(): string
  getTitle(): string
  isLoading(): boolean
  reload(): void
  reloadIgnoringCache(): void
  stop(): void
  findInPage(text: string, options?: WorkspaceBrowserFindOptions): number
  stopFindInPage(action: 'clearSelection'): void
  executeJavaScript(script: string): Promise<unknown>
  setWindowOpenHandler(handler: (details: { url: string }) => { action: 'deny' }): void
  on(event: 'did-start-loading', handler: () => void): unknown
  on(event: 'did-stop-loading', handler: () => void): unknown
  on(event: 'did-finish-load', handler: () => void): unknown
  on(event: 'did-start-navigation' | 'did-redirect-navigation', handler: (event: unknown, url: string, isInPlace: boolean, isMainFrame: boolean) => void): unknown
  on(event: 'zoom-changed', handler: (event: unknown, direction: string) => void): unknown
  on(event: 'before-input-event', handler: (event: { preventDefault(): void }, input: {
    type: string, key: string, code?: string, meta: boolean, control: boolean, shift: boolean, alt: boolean
  }) => void): unknown
  on(event: 'did-navigate', handler: (event: unknown, url: string) => void): unknown
  on(event: 'did-navigate-in-page', handler: (event: unknown, url: string, isMainFrame?: boolean) => void): unknown
  on(event: 'page-title-updated', handler: (event: unknown, title: string) => void): unknown
  on(
    event: 'did-fail-load',
    handler: (
      event: unknown,
      errorCode: number,
      errorDescription: string,
      validatedURL: string,
      isMainFrame: boolean,
    ) => void,
  ): unknown
  on(
    event: 'render-process-gone',
    handler: (event: unknown, details: { reason: string }) => void,
  ): unknown
  on(
    event: 'found-in-page',
    handler: (event: unknown, result: { activeMatchOrdinal: number, matches: number }) => void,
  ): unknown
  on(
    event: 'will-navigate',
    handler: (event: { preventDefault: () => void }, url: string) => void,
  ): unknown
  on(event: 'destroyed', handler: () => void): unknown
  navigationHistory?: WorkspaceBrowserNavigationHistoryLike
  canGoBack?(): boolean
  canGoForward?(): boolean
  goBack?(): void
  goForward?(): void
  setZoomFactor?(factor: number): void
  getZoomFactor?(): number
  capturePage?(): Promise<{ toDataURL(): string }>
  printToPDF?(options: Record<string, unknown>): Promise<Uint8Array>
  debugger?: WorkspaceBrowserDebuggerLike
  session?: WorkspaceBrowserSessionLike
  close?(): void
  isDestroyed?(): boolean
  isFocused?(): boolean
  isDevToolsOpened?(): boolean
}

export type WorkspaceBrowserParentWindowLike = {
  webContents?: {
    isDestroyed?(): boolean
    executeJavaScript?(script: string): Promise<unknown>
    getZoomFactor?(): number
    isDevToolsOpened?(): boolean
    debugger?: WorkspaceBrowserDebuggerLike
  }
  isDestroyed?(): boolean
  getContentBounds?(): { x: number, y: number, width: number, height: number }
  on?(event: 'resize' | 'move', listener: () => void): unknown
  removeListener?(event: 'resize' | 'move', listener: () => void): unknown
}

export type WorkspaceBrowserCreateOptions = {
  storageId: string
  url?: string
  /** The `<webview>` guest the renderer created for this page. */
  webContentsId: number
}

export type ElectronWorkspaceBrowserServiceOptions = {
  /**
   * Turns a renderer-reported id into the guest it names. It must throw for
   * anything that is not a workspace browser webview of the main window.
   */
  resolveGuest: (webContentsId: number) => WorkspaceBrowserWebContentsLike
  previewScriptPath: string
  emit: (event: WorkspaceBrowserEvent) => void
  /** Writes an exported PDF and resolves with the path it landed on. */
  writePdf?: (input: { data: Uint8Array, filename: string }) => Promise<string | null>
  platform?: NodeJS.Platform
  menuFactory?: WorkspaceBrowserMenuFactory
  /**
   * Runs a task after the current native dispatch. Electron applies its own
   * navigation zoom after `did-navigate` listeners return, so a correction made
   * inside the listener would be overwritten.
   */
  defer?: (task: () => void) => void
  /** Bounds a viewport capture; a hidden guest otherwise stalls for ~30s. */
  captureTimeoutMs?: number
}

type WorkspaceBrowserPage = {
  tabId: string
  storageId: string
  webContents: WorkspaceBrowserWebContentsLike
  webContentsId: number
  parent: WorkspaceBrowserParentWindowLike
  captureGeneration: number
  /** Whether the renderer currently shows this page. Only it can take input. */
  presented: boolean
  /** The guest starts on a blank document whose entry must not be "Back". */
  initialEntryCleared: boolean
  zoomFactor: number
  controls: PreviewBrowserControlsMessage | null
  controlsSignature: string | null
  pickerArmed: boolean
  persistentPicker: Extract<HostMessage, { type: 'enter-picker' }> | null
  pickerGeneration: number
  supportsPickerGeneration: boolean
  closed: boolean
  history: WorkspaceBrowserHistoryEntry[]
  fullCapture: Promise<string> | null
  captureCount: number
  navigationId: number
  navigationUrl: string
  navigationOutcome: 'idle' | 'pending' | 'succeeded' | 'failed'
  navigationCommitted: boolean
}

function isPlainRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isHostCaptureMessage(
  payload: unknown,
): payload is { v: 1, type: 'capture', kind: WorkspaceBrowserCaptureKind } {
  return isPlainRecord(payload) &&
    payload.v === 1 &&
    payload.type === 'capture' &&
    (payload.kind === 'full' || payload.kind === 'viewport')
}

function isHostPickerMessage(payload: unknown): payload is { v: 1, type: 'enter-picker' | 'exit-picker' } {
  return isPlainRecord(payload) &&
    payload.v === 1 &&
    (payload.type === 'enter-picker' || payload.type === 'exit-picker')
}

type CaptureRect = { x: number, y: number, width: number, height: number }
type FullCaptureHost = {
  width: number, height: number, dpr: number, scrollX: number, scrollY: number
  element: CaptureRect, connected: boolean, screen: string
}
type FullCaptureGuest = {
  documentCurrent: true
  width: number, height: number, dpr: number, scrollX: number, scrollY: number
  clientWidth: number, clientHeight: number, scrollWidth: number, scrollHeight: number
  bodyWidth: number, bodyHeight: number, body: CaptureRect, main: CaptureRect | null
  screen: string
}
type FullCaptureMetrics = { width: number, height: number, physicalWidth: number, physicalHeight: number, viewportWidth: number }
type FullCaptureSample = { host: FullCaptureHost, guest: FullCaptureGuest, metrics: FullCaptureMetrics }
type FullCapturePlan = { width: number, height: number, elementWidth: number, elementHeight: number, visibleWidth: number, visibleHeight: number, clipWidth: number, clipHeight: number }
type FullCaptureHostRequest = {
  action: 'begin' | 'snapshot' | 'resize' | 'restore' | 'release'
  webContentsId: number, token: string, width?: number, height?: number
}
type FullCaptureGuestRequest = {
  action: 'begin' | 'snapshot' | 'restore' | 'release'
  token: string, x?: number, y?: number
}

/** FIFO writers wait for all native readers; a writer also blocks new readers. */
class WorkspaceBrowserCaptureGate {
  private readers = 0
  private writer = false
  private readonly queue: Array<{ exclusive: boolean, resolve: (release: () => void) => void }> = []

  acquire(exclusive: boolean): Promise<() => void> {
    return new Promise(resolve => {
      this.queue.push({ exclusive, resolve })
      this.drain()
    })
  }

  private drain(): void {
    if (this.writer) return
    while (this.queue.length > 0) {
      const next = this.queue[0]!
      if (next.exclusive && this.readers > 0) return
      this.queue.shift()
      if (next.exclusive) this.writer = true
      else this.readers += 1
      let released = false
      next.resolve(() => {
        if (released) return
        released = true
        if (next.exclusive) this.writer = false
        else this.readers -= 1
        this.drain()
      })
      if (next.exclusive) return
    }
  }
}

/** Runs in the trusted embedder. The token holds the original element, never a replacement tab. */
function fullCaptureHostOperation(input: FullCaptureHostRequest): unknown {
  type ElementRecord = {
    element: HTMLElement & { getWebContentsId(): number }
    width: string, widthPriority: string, height: string, heightPriority: string
    originalAttribute: string | null, resizedWidth?: string, resizedHeight?: string
  }
  const key = Symbol.for('cc-haha.workspace-browser.capture')
  const root = globalThis as unknown as Record<symbol, Map<string, ElementRecord> | undefined>
  const records = root[key] ??= new Map<string, ElementRecord>()
  if (input.action === 'release') {
    records.delete(input.token)
    if (records.size === 0) delete root[key]
    return { released: true }
  }
  if (input.action === 'begin') {
    if (!Number.isSafeInteger(input.webContentsId) || input.webContentsId <= 0 || records.has(input.token)) throw new Error('invalid capture owner')
    const matches = Array.from(document.querySelectorAll('webview')).filter(candidate => {
      const element = candidate as HTMLElement & { getWebContentsId?(): number }
      try { return element.isConnected && element.getWebContentsId?.() === input.webContentsId } catch { return false }
    })
    if (matches.length !== 1) throw new Error('capture guest element is not uniquely owned')
    const element = matches[0] as HTMLElement & { getWebContentsId(): number }
    const { style } = element
    records.set(input.token, {
      element, width: style.getPropertyValue('width'), widthPriority: style.getPropertyPriority('width'),
      height: style.getPropertyValue('height'), heightPriority: style.getPropertyPriority('height'),
      originalAttribute: element.getAttribute('style'),
    })
  }
  const record = records.get(input.token)
  if (!record) throw new Error('capture element token was lost')
  const element = record.element
  if (input.action === 'resize') {
    if (!element.isConnected || element.getWebContentsId() !== input.webContentsId) throw new Error('capture guest element was replaced')
    if (!Number.isFinite(input.width) || !Number.isFinite(input.height) || input.width! <= 0 || input.height! <= 0) throw new Error('invalid capture element size')
    // Validate both properties before changing either the ownership record or
    // CSS. This also protects the gap between the two resize stages.
    for (const property of ['width', 'height'] as const) {
      const resized = property === 'width' ? record.resizedWidth : record.resizedHeight
      const expected = resized ?? record[property]
      const priority = resized === undefined
        ? property === 'width' ? record.widthPriority : record.heightPriority
        : 'important'
      if (element.style.getPropertyValue(property) !== expected || element.style.getPropertyPriority(property) !== priority) {
        throw new Error(`capture element size was changed by another owner before resize: ${property}`)
      }
    }
    element.style.setProperty('width', `${input.width}px`, 'important')
    record.resizedWidth = element.style.getPropertyValue('width')
    element.style.setProperty('height', `${input.height}px`, 'important')
    record.resizedHeight = element.style.getPropertyValue('height')
  }
  if (input.action === 'restore') {
    // Restore only our two properties, preserving unrelated styles added meanwhile.
    // A removed element is safe to restore; it can never name a new guest.
    const conflicts: string[] = []
    for (const property of ['width', 'height'] as const) {
      const expected = property === 'width' ? record.resizedWidth : record.resizedHeight
      // A failed precondition did not authorize any write to this property.
      if (expected === undefined) continue
      if (element.style.getPropertyValue(property) !== expected || element.style.getPropertyPriority(property) !== 'important') {
        conflicts.push(property)
        continue
      }
      const priority = property === 'width' ? record.widthPriority : record.heightPriority
      if (record[property]) element.style.setProperty(property, record[property], priority)
      else element.style.removeProperty(property)
    }
    if (record.originalAttribute === null && element.style.length === 0) element.removeAttribute('style')
    if (conflicts.length > 0) throw new Error(`capture element size was changed by another owner: ${conflicts.join(', ')}`)
  }
  const rect = element.getBoundingClientRect()
  return {
    width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollX, scrollY,
    element: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }, connected: element.isConnected,
    screen: JSON.stringify([screen.width, screen.height, screen.availWidth, screen.availHeight, screen.colorDepth, screen.pixelDepth]),
  }
}

function scriptForFullCaptureHost(input: FullCaptureHostRequest): string {
  return `(${fullCaptureHostOperation.toString()})(${JSON.stringify(input)})`
}

/** Runs in the guest; rectangles use document coordinates so scroll is independent. */
function fullCaptureGuestGeometry(input: FullCaptureGuestRequest): unknown {
  type DocumentRecord = { document: Document }
  const key = Symbol.for('cc-haha.workspace-browser.capture-document')
  const root = globalThis as unknown as Record<symbol, Map<string, DocumentRecord> | undefined>
  if (input.action === 'release') {
    // A queued release may run in a replacement realm. Never initialize state
    // there, or delete a token belonging to any other capture.
    const records = root[key]
    if (records?.has(input.token)) {
      records.delete(input.token)
      if (records.size === 0) delete root[key]
    }
    return { released: true }
  }
  if (input.action === 'begin') {
    if (typeof input.token !== 'string' || !input.token || root[key]?.has(input.token)) throw new Error('invalid capture document token')
    const records = root[key] ??= new Map<string, DocumentRecord>()
    records.set(input.token, { document })
  }
  if (root[key]?.get(input.token)?.document !== document) {
    if (input.action === 'restore') return { documentCurrent: false }
    throw new Error('full browser capture document changed')
  }
  if (input.action === 'restore') window.scrollTo({ left: input.x, top: input.y, behavior: 'instant' })
  const rect = (element: Element | null) => {
    if (!element) return null
    const value = element.getBoundingClientRect()
    return { x: value.x + scrollX, y: value.y + scrollY, width: value.width, height: value.height }
  }
  const html = document.documentElement
  const body = document.body
  if (!body) throw new Error('capture document has no body')
  return {
    documentCurrent: true,
    width: innerWidth, height: innerHeight, dpr: devicePixelRatio, scrollX, scrollY,
    clientWidth: html.clientWidth, clientHeight: html.clientHeight, scrollWidth: html.scrollWidth, scrollHeight: html.scrollHeight,
    bodyWidth: body.scrollWidth, bodyHeight: body.scrollHeight, body: rect(body), main: rect(document.querySelector('main')),
    screen: JSON.stringify([screen.width, screen.height, screen.availWidth, screen.availHeight, screen.colorDepth, screen.pixelDepth]),
  }
}

function scriptForFullCaptureGuest(input: FullCaptureGuestRequest): string {
  return `(${fullCaptureGuestGeometry.toString()})(${JSON.stringify(input)})`
}

function parseCaptureRect(value: unknown): CaptureRect {
  if (!isPlainRecord(value) || !['x', 'y', 'width', 'height'].every(key => typeof value[key] === 'number' && Number.isFinite(value[key]))) {
    throw new Error('invalid capture rectangle')
  }
  return value as CaptureRect
}

function parseCaptureNumbers(value: unknown, keys: string[]): asserts value is Record<string, unknown> {
  if (!isPlainRecord(value) || !keys.every(key => typeof value[key] === 'number' && Number.isFinite(value[key]))) throw new Error('invalid capture geometry')
}

function parseFullCaptureHost(value: unknown): FullCaptureHost {
  parseCaptureNumbers(value, ['width', 'height', 'dpr', 'scrollX', 'scrollY'])
  parseCaptureRect(value.element)
  if (typeof value.connected !== 'boolean' || typeof value.screen !== 'string') throw new Error('invalid capture host geometry')
  return value as unknown as FullCaptureHost
}

function parseFullCaptureGuest(value: unknown): FullCaptureGuest {
  parseCaptureNumbers(value, ['width', 'height', 'dpr', 'scrollX', 'scrollY', 'clientWidth', 'clientHeight', 'scrollWidth', 'scrollHeight', 'bodyWidth', 'bodyHeight'])
  parseCaptureRect(value.body)
  if (value.main !== null) parseCaptureRect(value.main)
  if (value.documentCurrent !== true || typeof value.screen !== 'string') throw new Error('invalid capture guest geometry')
  return value as unknown as FullCaptureGuest
}

function parseFullCaptureMetrics(value: unknown): FullCaptureMetrics {
  if (!isPlainRecord(value) || !isPlainRecord(value.cssContentSize) || !isPlainRecord(value.contentSize) ||
    !isPlainRecord(value.cssLayoutViewport) || !isPlainRecord(value.cssVisualViewport) || value.cssVisualViewport.scale !== 1) {
    throw new Error('unsupported full capture layout metrics')
  }
  const layoutViewportWidth = Number(value.cssLayoutViewport.clientWidth)
  const result = {
    width: Number(value.cssContentSize.width), height: Number(value.cssContentSize.height),
    physicalWidth: Number(value.contentSize.width), physicalHeight: Number(value.contentSize.height),
    viewportWidth: Number(value.cssVisualViewport.clientWidth),
  }
  if (!Object.values(result).every(number => Number.isFinite(number) && number > 0) ||
    !Number.isFinite(layoutViewportWidth) || layoutViewportWidth <= 0 ||
    Math.abs(result.viewportWidth - layoutViewportWidth) >= 1) throw new Error('invalid full capture dimensions')
  checkFullCaptureBudget(Math.ceil(result.width), Math.ceil(result.height))
  return result
}

function checkFullCaptureBudget(width: number, height: number): void {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0 ||
    width > FULL_CAPTURE_MAX_EDGE || height > FULL_CAPTURE_MAX_EDGE || width * height > FULL_CAPTURE_MAX_PIXELS) {
    throw new Error(`full capture exceeds safety limit: ${width}x${height}`)
  }
}

function nearlyEqual(a: number, b: number, tolerance = 0.01): boolean {
  return Number.isFinite(a) && Number.isFinite(b) && Math.abs(a - b) <= tolerance
}

function sameCaptureBounds(a: CaptureRect, b: CaptureRect): boolean {
  return ['x', 'y', 'width', 'height'].every(key => nearlyEqual(a[key as keyof CaptureRect], b[key as keyof CaptureRect]))
}

function sameCaptureGeometry(a: FullCaptureHost | FullCaptureGuest, b: FullCaptureHost | FullCaptureGuest): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

function sameCaptureMetrics(a: FullCaptureMetrics, b: FullCaptureMetrics): boolean {
  return Object.keys(a).every(key => nearlyEqual(a[key as keyof FullCaptureMetrics], b[key as keyof FullCaptureMetrics]))
}

function planFullCapture(value: FullCaptureSample, hostZoom: number, guestZoom: number | undefined, bounds: CaptureRect): FullCapturePlan {
  if (!Number.isFinite(hostZoom) || hostZoom <= 0 || !guestZoom || !Number.isFinite(guestZoom) || guestZoom <= 0 ||
    !value.host.connected || value.host.dpr <= 0 || value.guest.dpr <= 0 ||
    !nearlyEqual(value.host.dpr / hostZoom, value.guest.dpr / guestZoom) ||
    !nearlyEqual(value.host.width * hostZoom, bounds.width, hostZoom) ||
    !nearlyEqual(value.host.height * hostZoom, bounds.height, hostZoom)) throw new Error('unsupported browser capture zoom geometry')
  const physicalWidth = value.metrics.physicalWidth / value.guest.dpr
  const physicalHeight = value.metrics.physicalHeight / value.guest.dpr
  for (const residual of [physicalWidth - value.metrics.width, physicalHeight - value.metrics.height]) {
    if (residual < -0.01 || residual >= 1) throw new Error('unsupported browser capture CSS/device pixel conversion')
  }
  // Keep the original content column when its vertical scrollbar disappears.
  // Horizontal overflow cannot be expanded without changing layout; refuse it.
  const width = value.metrics.viewportWidth
  const height = Math.max(value.guest.height, Math.ceil(physicalHeight))
  if (!nearlyEqual(value.metrics.width, width, 1) || !nearlyEqual(value.guest.clientWidth, width, 1)) {
    throw new Error('full browser capture cannot preserve horizontal overflow layout')
  }
  const elementWidth = width * guestZoom / hostZoom
  const elementHeight = height * guestZoom / hostZoom
  const visibleWidth = Math.ceil(bounds.width)
  const visibleHeight = Math.max(Math.ceil(bounds.height), Math.ceil(value.host.element.y * hostZoom + height * guestZoom))
  // CDP's clip dimensions are widget DIPs. Round the complete canvas outward;
  // clip.scale also scales rendering and must not apply browser zoom again.
  const clipWidth = Math.ceil(width * guestZoom)
  const clipHeight = Math.ceil(height * guestZoom)
  const guestBaseDsf = value.guest.dpr / guestZoom
  const hostBaseDsf = value.host.dpr / hostZoom
  checkFullCaptureBudget(Math.ceil(width), Math.ceil(height))
  checkFullCaptureBudget(visibleWidth, visibleHeight)
  checkFullCaptureBudget(Math.ceil(visibleWidth * hostBaseDsf), Math.ceil(visibleHeight * hostBaseDsf))
  checkFullCaptureBudget(Math.ceil(width * value.guest.dpr), Math.ceil(height * value.guest.dpr))
  checkFullCaptureBudget(Math.ceil(clipWidth * guestBaseDsf), Math.ceil(clipHeight * guestBaseDsf))
  return { width, height, elementWidth, elementHeight, visibleWidth, visibleHeight, clipWidth, clipHeight }
}

function fullCaptureHeightReady(before: FullCaptureSample, current: FullCaptureSample, plan: FullCapturePlan): boolean {
  const a = before.guest
  const b = current.guest
  // Remove the original vertical scrollbar before narrowing to its content
  // column. Narrowing while that scrollbar exists can wrap text, grow the
  // document and keep the scrollbar alive at the planned full height.
  return current.host.connected && current.host.screen === before.host.screen && b.screen === a.screen &&
    nearlyEqual(current.host.dpr, before.host.dpr) && nearlyEqual(current.host.width, before.host.width, 1) &&
    nearlyEqual(current.host.height, before.host.height, 1) &&
    nearlyEqual(current.host.scrollX, before.host.scrollX) && nearlyEqual(current.host.scrollY, before.host.scrollY) &&
    nearlyEqual(current.host.element.x, before.host.element.x) && nearlyEqual(current.host.element.y, before.host.element.y) &&
    nearlyEqual(current.host.element.width, before.host.element.width, 1) && nearlyEqual(current.host.element.height, plan.elementHeight, 1) &&
    nearlyEqual(b.dpr, a.dpr) && nearlyEqual(b.width, a.width, 1) &&
    nearlyEqual(b.height, plan.height, 1) && nearlyEqual(b.clientHeight, plan.height, 1) &&
    nearlyEqual(b.width, b.clientWidth, 1) && nearlyEqual(current.metrics.viewportWidth, b.clientWidth, 1) &&
    b.scrollWidth <= b.clientWidth + 1 && b.scrollHeight <= b.clientHeight + 1
}

function fullCaptureLayoutPreserved(before: FullCaptureSample, current: FullCaptureSample, plan: FullCapturePlan): boolean {
  const a = before.guest
  const b = current.guest
  return current.host.connected && current.host.screen === before.host.screen && b.screen === a.screen &&
    nearlyEqual(current.host.dpr, before.host.dpr) && nearlyEqual(current.host.width, before.host.width, 1) &&
    nearlyEqual(current.host.scrollX, before.host.scrollX) && nearlyEqual(current.host.scrollY, before.host.scrollY) &&
    nearlyEqual(current.host.element.x, before.host.element.x) && nearlyEqual(current.host.element.y, before.host.element.y) &&
    nearlyEqual(current.host.element.width, plan.elementWidth, 1) && nearlyEqual(current.host.element.height, plan.elementHeight, 1) &&
    nearlyEqual(b.dpr, a.dpr) && nearlyEqual(b.width, plan.width, 1) && nearlyEqual(b.height, plan.height, 1) &&
    nearlyEqual(b.clientWidth, plan.width, 1) && nearlyEqual(b.clientHeight, plan.height, 1) &&
    nearlyEqual(b.scrollWidth, a.scrollWidth) && nearlyEqual(b.scrollHeight, Math.max(a.scrollHeight, plan.height), 1) &&
    nearlyEqual(b.bodyWidth, a.bodyWidth) && nearlyEqual(b.bodyHeight, a.bodyHeight) &&
    JSON.stringify(b.body) === JSON.stringify(a.body) && JSON.stringify(b.main) === JSON.stringify(a.main) &&
    nearlyEqual(current.metrics.width, before.metrics.width, 1) && nearlyEqual(current.metrics.height, Math.max(before.metrics.height, plan.height), 1)
}

export function workspaceBrowserPdfFilename(url: string, title: string): string {
  const raw = title.trim() || (() => {
    try {
      return new URL(url).hostname
    } catch {
      return 'page'
    }
  })()
  const safe = raw.replace(/[^\w.-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 80)
  return `${safe || 'page'}.pdf`
}

/**
 * Multi-page browser host.
 *
 * Each page is a `<webview>` guest the renderer keeps in a layer that is never
 * unmounted, so the page is composited with the DOM: menus, dialogs and other
 * pages draw over it like over any element. This service adopts the guest and
 * owns everything that happens *inside* it — navigation, history, find, zoom,
 * capture, downloads and the annotation agent.
 *
 * A page's lifetime is still decided by `close(tabId)` and nothing else.
 * Hiding a tab, moving the panel or unmounting the React surface only change
 * whether a page is drawn; the guest keeps its form state, scroll position and
 * navigation history the entire time.
 */
export class ElectronWorkspaceBrowserService {
  private readonly resolveGuest: (webContentsId: number) => WorkspaceBrowserWebContentsLike
  private readonly previewScriptPath: string
  private readonly emit: (event: WorkspaceBrowserEvent) => void
  private readonly writePdf?: (input: { data: Uint8Array, filename: string }) => Promise<string | null>
  private readonly platform: NodeJS.Platform
  private readonly defer: (task: () => void) => void
  private readonly captureTimeoutMs: number
  private readonly pages = new Map<string, WorkspaceBrowserPage>()
  private readonly hookedSessions = new Set<WorkspaceBrowserSessionLike>()
  private readonly captureGates = new WeakMap<WorkspaceBrowserParentWindowLike, WorkspaceBrowserCaptureGate>()
  private readonly unsafeCaptureParents = new WeakSet<WorkspaceBrowserParentWindowLike>()
  /**
   * Page zoom as Chromium keeps it: per host, shared by every page of the
   * partition. Electron overwrites a guest's zoom with its embedder's whenever
   * app zoom changes or the guest navigates, so this is the value to restore.
   */
  private readonly zoomByHost = new Map<string, number>()
  private parent: WorkspaceBrowserParentWindowLike | null = null
  private downloadSequence = 0
  private readonly menu?: WorkspaceBrowserMenuController

  constructor(options: ElectronWorkspaceBrowserServiceOptions) {
    this.resolveGuest = options.resolveGuest
    this.previewScriptPath = options.previewScriptPath
    this.emit = options.emit
    this.writePdf = options.writePdf
    this.platform = options.platform ?? process.platform
    this.defer = options.defer ?? (task => { setImmediate(task) })
    this.captureTimeoutMs = options.captureTimeoutMs ?? VIEWPORT_CAPTURE_TIMEOUT_MS
    if (options.menuFactory) this.menu = new WorkspaceBrowserMenuController(options.menuFactory)
  }

  async create(
    parent: WorkspaceBrowserParentWindowLike,
    tabId: string,
    options: WorkspaceBrowserCreateOptions,
  ): Promise<void> {
    // Validate before anything is registered: a throw after `openPage` would
    // leave a page this service can neither address nor release.
    const url = options.url ? normalizePreviewUrl(options.url) : null
    const webContents = this.resolveGuest(options.webContentsId)

    this.parent = parent
    const existing = this.pages.get(tabId)
    if (existing) {
      // A live page is NEVER re-navigated from `create`. The renderer asks
      // again whenever it has to re-establish a page, and `loadURL` on a live
      // guest is a hard navigation: it would wipe the form the user filled in,
      // reset the scroll position and push a duplicate back entry.
      if (existing.webContents !== webContents) {
        throw new Error(`workspace browser tab already has a live page: ${tabId}`)
      }
      this.emitState(existing)
      return
    }
    const owner = this.findPageByWebContents(webContents)
    if (owner) throw new Error(`workspace browser guest already belongs to ${owner.tabId}`)

    const page = this.openPage(tabId, options.storageId, webContents, options.webContentsId, parent)
    // Resolving means "registered": the renderer may now send visibility,
    // Stop and navigation. The first load is an ordinary navigation, so its
    // failure reaches the tab as a `failed` event like any other — answering
    // `create` with it would make an adopted page look unregistered.
    this.emitState(page)
    if (url) {
      void webContents.loadURL(url).catch(() => {
        // Reported through `did-fail-load`, or superseded by a newer navigation.
      })
    }
  }

  async navigate(tabId: string, url: string): Promise<void> {
    const page = this.requirePage(tabId)
    page.pickerArmed = false
    page.persistentPicker = null
    page.pickerGeneration += 1
    await page.webContents.loadURL(normalizePreviewUrl(url))
  }

  async showMenu(
    parent: WorkspaceBrowserParentWindowLike,
    tabId: string,
    options: WorkspaceBrowserMenuOptions,
  ): Promise<WorkspaceBrowserMenuAction | null> {
    const page = this.requirePage(tabId)
    if (parent !== this.parent || parent.isDestroyed?.() || parent.webContents?.isDestroyed?.()) {
      throw new Error('Workspace browser menu requires its live owner window')
    }
    if (!this.menu) throw new Error('Workspace browser native menu unavailable')
    const nativeZoom = page.webContents.getZoomFactor?.()
    if (nativeZoom !== undefined && Number.isFinite(nativeZoom) && nativeZoom > 0 && nativeZoom !== page.zoomFactor) {
      page.zoomFactor = nativeZoom
      // Menu actions return to the renderer. Its next zoom step must start
      // from the same current native value that the menu displays.
      this.emitState(page)
    }
    return this.menu.show(tabId, { ...options, zoomFactor: page.zoomFactor })
  }

  goBack(tabId: string): void {
    const webContents = this.requirePage(tabId).webContents
    if (webContents.navigationHistory) {
      webContents.navigationHistory.goBack()
      return
    }
    webContents.goBack?.()
  }

  goForward(tabId: string): void {
    const webContents = this.requirePage(tabId).webContents
    if (webContents.navigationHistory) {
      webContents.navigationHistory.goForward()
      return
    }
    webContents.goForward?.()
  }

  reload(tabId: string, options?: { ignoreCache?: boolean }): void {
    const webContents = this.requirePage(tabId).webContents
    if (options?.ignoreCache) webContents.reloadIgnoringCache()
    else webContents.reload()
  }

  stop(tabId: string): void {
    this.requirePage(tabId).webContents.stop()
  }

  /**
   * Records whether the renderer shows this page. Drawing is the renderer's
   * job; this decides which page may act as browser chrome (shortcuts, zoom
   * capsule) and closes a native menu that belonged to a page going away.
   */
  setVisible(tabId: string, visible: boolean): void {
    // Controller close may precede the component's passive unmount cleanup.
    // Only hide is an idempotent teardown; showing a missing page is still an error.
    if (!visible && !this.pages.has(tabId)) return
    const page = this.requirePage(tabId)
    if (page.presented !== visible) page.captureGeneration += 1
    page.presented = visible
    if (!visible) {
      this.menu?.cancel(page.tabId)
      return
    }
    // Chromium may have changed this page's zoom while it was off screen (a
    // same-host page zoomed); the controls it shows must read the real value.
    this.emitState(page)
  }

  setZoom(tabId: string, factor: unknown): void {
    const page = this.requirePage(tabId)
    // Chromium shares page zoom by origin; every pending image may be affected.
    for (const current of this.pages.values()) current.captureGeneration += 1
    page.zoomFactor = normalizeZoomFactor(factor)
    this.zoomByHost.set(zoomHostKey(page.webContents.getURL()), page.zoomFactor)
    page.webContents.setZoomFactor?.(page.zoomFactor)
    // Chromium may apply zoom to another live page on the same origin.
    // Read every native value so controls never report an invented factor.
    for (const current of this.pages.values()) this.emitState(current)
  }

  /**
   * Electron copies the embedder's zoom into every guest when app zoom
   * changes. App zoom never scaled the old native page, so put each page back
   * at the zoom it had. Must run right after the embedder zoom changes.
   */
  restorePageZoom(): void {
    for (const page of this.pages.values()) {
      if (page.closed || page.webContents.isDestroyed?.()) continue
      page.captureGeneration += 1
      page.webContents.setZoomFactor?.(page.zoomFactor)
    }
  }

  find(tabId: string, text: string, options?: WorkspaceBrowserFindOptions): void {
    const trimmed = text.trim()
    const page = this.requirePage(tabId)
    if (!trimmed) {
      page.webContents.stopFindInPage('clearSelection')
      return
    }
    page.webContents.findInPage(trimmed, options)
  }

  stopFind(tabId: string): void {
    this.requirePage(tabId).webContents.stopFindInPage('clearSelection')
  }

  async capture(tabId: string, kind: WorkspaceBrowserCaptureKind): Promise<void> {
    const page = this.requirePage(tabId)
    const navigationId = page.navigationId
    const generation = page.captureGeneration
    const dataUrl = await this.captureDataUrl(page, kind)
    this.assertCaptureCurrent(page, navigationId, generation)
    this.emitFor(page, { type: 'screenshot', tabId: page.tabId, dataUrl, kind })
  }

  async message(tabId: string, payload: unknown): Promise<void> {
    const page = this.requirePage(tabId)
    const controls = parseHostMessage(JSON.stringify(payload))
    if (controls?.type === 'browser-controls') {
      page.controls = { v: 1, ...controls }
      await this.syncBrowserControls(page)
      return
    }
    if (isHostCaptureMessage(payload)) {
      await this.capture(tabId, payload.kind)
      return
    }
    if (isHostPickerMessage(payload)) {
      if (!controls || (controls.type !== 'enter-picker' && controls.type !== 'exit-picker')) return
      page.pickerGeneration += 1
      page.pickerArmed = controls.type === 'enter-picker'
      page.persistentPicker = controls.type === 'enter-picker' && controls.persistent ? controls : null
      this.emitState(page)
    }
    const raw = JSON.stringify(isHostPickerMessage(payload) ? { ...payload, generation: page.pickerGeneration } : payload)
    const generation = page.pickerGeneration
    try {
      await page.webContents.executeJavaScript(
        `globalThis.__PREVIEW_BRIDGE__?.handleHostRaw(${JSON.stringify(raw)})`,
      )
    } catch (error) {
      if (isHostPickerMessage(payload) && generation === page.pickerGeneration) {
        page.pickerArmed = false
        page.persistentPicker = null
        page.pickerGeneration += 1
        this.emitState(page)
      }
      throw error
    }
  }

  async printToPdf(tabId: string): Promise<void> {
    const page = this.requirePage(tabId)
    const webContents = page.webContents
    if (!webContents.printToPDF || !this.writePdf) throw new Error('pdf export unavailable')
    const filename = workspaceBrowserPdfFilename(webContents.getURL(), webContents.getTitle())
    const data = await webContents.printToPDF({ printBackground: true })
    const savePath = await this.writePdf({ data, filename })
    if (!savePath) return
    this.emit({
      type: 'download',
      tabId: page.tabId,
      download: {
        id: this.nextDownloadId(),
        filename: basename(savePath),
        savePath,
        receivedBytes: data.byteLength,
        totalBytes: data.byteLength,
        state: 'completed',
      },
    })
  }

  /**
   * The only call that ends a page's life. The renderer removes the guest's
   * element as well; closing here too means a page can never outlive its tab
   * even if that removal never happens.
   */
  close(tabId: string): void {
    const page = this.pages.get(tabId)
    if (!page) return
    this.pages.delete(tabId)
    page.closed = true
    this.menu?.cancel(page.tabId)
    if (!page.webContents.isDestroyed?.()) {
      page.webContents.close?.()
    }
  }

  closeAll(): void {
    this.menu?.cancel()
    for (const tabId of [...this.pages.keys()]) this.close(tabId)
    this.parent = null
  }

  /**
   * Routes an in-page agent message. Returns whether this service owns the
   * sender, so the caller can fall through to the legacy singleton preview.
   */
  handleMessageFromView(sender: unknown, raw: unknown): boolean {
    const page = this.findPageByWebContents(sender)
    if (!page) return false
    if (typeof raw === 'string') void this.deliverAgentMessage(page, raw)
    return true
  }

  private openPage(
    tabId: string,
    storageId: string,
    webContents: WorkspaceBrowserWebContentsLike,
    webContentsId: number,
    parent: WorkspaceBrowserParentWindowLike,
  ): WorkspaceBrowserPage {
    const page: WorkspaceBrowserPage = {
      tabId,
      storageId,
      webContents,
      webContentsId,
      parent,
      captureGeneration: 0,
      presented: false,
      initialEntryCleared: false,
      zoomFactor: 1,
      controls: null,
      controlsSignature: null,
      pickerArmed: false,
      persistentPicker: null,
      pickerGeneration: 0,
      supportsPickerGeneration: false,
      closed: false,
      history: [],
      fullCapture: null,
      captureCount: 0,
      navigationId: 0,
      navigationUrl: '',
      navigationOutcome: 'idle',
      navigationCommitted: false,
    }
    this.pages.set(tabId, page)
    this.installPageListeners(page)
    this.hookSession(webContents.session)
    // The guest attached at its embedder's zoom; a page starts at 100% or at
    // whatever its host was last zoomed to, exactly as a fresh native page did.
    this.restoreNavigationZoom(page)
    return page
  }

  private installPageListeners(page: WorkspaceBrowserPage): void {
    const webContents = page.webContents

    // Popups become tabs in this window instead of native child windows, which
    // would escape the workspace and the preload/permission boundary with it.
    webContents.setWindowOpenHandler(({ url }) => {
      if (isHttpUrl(url)) this.emitFor(page, { type: 'new-window', tabId: page.tabId, url })
      return { action: 'deny' }
    })
    webContents.on('will-navigate', (event, url) => {
      if (!isHttpUrl(url)) event.preventDefault()
    })
    webContents.on('destroyed', () => {
      // A guest dies with its element. Unless the tab closed it, the renderer
      // lost the page (its layer went away) and the tab must offer a retry
      // instead of addressing a page that no longer exists.
      if (page.closed || this.pages.get(page.tabId) !== page) return
      this.pages.delete(page.tabId)
      page.closed = true
      this.menu?.cancel(page.tabId)
      this.emit({ type: 'destroyed', tabId: page.tabId, reason: 'closed' })
    })

    webContents.on('before-input-event', (event, input) => {
      if (input.type !== 'keyDown' || !page.presented || page.closed) return
      const action = matchWorkspaceShortcut({
        key: input.key, code: input.code, metaKey: input.meta,
        ctrlKey: input.control, shiftKey: input.shift, altKey: input.alt,
      }, { platform: this.platform === 'darwin' ? 'mac' : 'other', context: 'browser' })
      if (!action) return
      // Prevent both the page event and the menu accelerator. The renderer's
      // owner-aware controller executes the same action as a DOM shortcut.
      event.preventDefault()
      this.emitFor(page, { type: 'shortcut', tabId: page.tabId, action })
    })
    webContents.on('did-start-navigation', (_event, url, isInPlace, isMainFrame) => {
      if (!isMainFrame) return
      if (isInPlace) {
        // Hash/history navigation keeps this document (and its event handlers)
        // alive. Exit the page picker too; clearing only host state strands it.
        void this.message(page.tabId, { v: 1, type: 'exit-picker' }).catch(error => {
          if (!page.closed) console.error('Failed to exit annotation after in-page navigation', error)
        })
      } else {
        page.pickerArmed = false
        page.persistentPicker = null
        page.pickerGeneration += 1
      }
      page.controlsSignature = null
      page.navigationId += 1
      page.navigationUrl = url
      page.navigationOutcome = 'pending'
      page.navigationCommitted = false
      this.emitState(page)
    })
    webContents.on('did-redirect-navigation', (_event, url, _isInPlace, isMainFrame) => {
      if (isMainFrame) page.navigationUrl = url
    })
    webContents.on('zoom-changed', () => {
      for (const current of this.pages.values()) {
        current.captureGeneration += 1
        this.emitState(current)
      }
    })

    webContents.on('did-start-loading', () => this.emitState(page))
    webContents.on('did-stop-loading', () => this.emitState(page))
    webContents.on('page-title-updated', () => this.emitState(page))
    webContents.on('did-navigate', (_event, url) => {
      page.pickerArmed = false
      page.persistentPicker = null
      page.pickerGeneration += 1
      page.navigationUrl = url
      page.navigationCommitted = true
      if (!page.initialEntryCleared && url !== WORKSPACE_BROWSER_INITIAL_SRC) {
        // The blank document the guest attached with is a real history entry;
        // a native page had none, so the first page must not be able to go
        // "Back" to it.
        page.initialEntryCleared = true
        webContents.navigationHistory?.clear?.()
      }
      this.recordVisit(page, url)
      this.emitState(page)
      // Electron re-applies the embedder's zoom once these listeners return.
      this.defer(() => this.restoreNavigationZoom(page))
    })
    webContents.on('did-navigate-in-page', (_event, url, isMainFrame) => {
      if (isMainFrame === false) return
      page.navigationUrl = url
      page.navigationOutcome = 'succeeded'
      this.recordVisit(page, url)
      this.emitState(page)
    })
    webContents.on('did-finish-load', () => {
      if (page.navigationOutcome === 'pending' && page.navigationCommitted) {
        page.navigationOutcome = 'succeeded'
      }
      void this.injectPreviewAgent(page).catch(error => {
        if (!page.closed) console.error('Failed to initialize workspace browser controls', error)
      })
      this.emitState(page)
    })
    webContents.on('did-fail-load', (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
      // Subframe failures are normal on the open web and must not put the whole
      // tab into an error state.
      if (!isMainFrame || errorCode === -3 || page.navigationOutcome === 'succeeded') return
      if (page.navigationUrl && validatedURL !== page.navigationUrl) return
      page.navigationOutcome = 'failed'
      this.emitFor(page, {
        type: 'failed',
        tabId: page.tabId,
        url: validatedURL,
        errorCode,
        errorDescription,
        navigationId: page.navigationId,
      })
    })
    webContents.on('render-process-gone', () => {
      this.emitFor(page, { type: 'destroyed', tabId: page.tabId, reason: 'crashed' })
    })
    webContents.on('found-in-page', (_event, result) => {
      this.emitFor(page, {
        type: 'found',
        tabId: page.tabId,
        activeMatchOrdinal: result.activeMatchOrdinal,
        matches: result.matches,
      })
    })
  }

  private hookSession(session: WorkspaceBrowserSessionLike | undefined): void {
    if (!session || this.hookedSessions.has(session)) return
    this.hookedSessions.add(session)
    // Downloads belong to the shared session, so the main process owns their
    // lifetime. Keep the source id for attribution after its page is closed.
    session.on('will-download', (_event, item, webContents) => {
      const page = this.findPageByWebContents(webContents)
      if (!page) return
      const id = this.nextDownloadId()
      const report = () => {
        this.emit({
          type: 'download',
          tabId: page.tabId,
          download: {
            id,
            filename: item.getFilename(),
            savePath: item.getSavePath() || null,
            receivedBytes: item.getReceivedBytes(),
            totalBytes: item.getTotalBytes(),
            state: normalizeDownloadState(item.getState()),
          },
        })
      }
      item.on('updated', report)
      item.on('done', report)
      report()
    })
  }

  private async deliverAgentMessage(page: WorkspaceBrowserPage, raw: string): Promise<void> {
    const navigationId = page.navigationId
    const pickerGeneration = page.pickerGeneration
    const captureGeneration = page.captureGeneration
    const persistentPicker = page.persistentPicker
    const message = parsePreviewAgentMessage(raw)
    if (!message) return
    if (message.type === 'ready' && message.supportsPickerGeneration === true) page.supportsPickerGeneration = true
    if (message.type === 'selection' || message.type === 'picker-exited') {
      if (message.generation !== undefined
        ? message.generation !== page.pickerGeneration
        : page.supportsPickerGeneration || page.persistentPicker !== null) return
    }
    if (message.type === 'browser-zoom') {
      // Only the attached page can act as browser chrome. Background pages
      // cannot modify another tab, and no zoom event enters the chat pipeline.
      if (page.presented && page.controls) {
        const current = page.webContents.getZoomFactor?.() ?? page.zoomFactor
        this.setZoom(page.tabId, message.action === 'reset' ? 1 : Math.round((current + (message.action === 'in' ? 0.1 : -0.1)) * 10) / 10)
      }
      return
    }
    if (message.type === 'selection') {
      // Consume before the asynchronous native capture so a page cannot replay
      // selection events while the first capture is in flight.
      if (!page.pickerArmed) return
      page.pickerArmed = false
    } else if (message.type === 'picker-exited') {
      page.pickerArmed = false
      page.persistentPicker = null
      page.pickerGeneration += 1
      this.emitState(page)
    }
    const payload = message.type === 'selection'
      ? await this.withNativeSelectionScreenshot(page, message)
      : message
    if (navigationId !== page.navigationId || captureGeneration !== page.captureGeneration) return
    this.emitFor(page, { type: 'agent', tabId: page.tabId, message: message.type === 'selection' && persistentPicker
      ? { ...payload, persistent: true } : payload })
    // A selection still consumes exactly one authorization. Continue only once
    // its native screenshot has finished, and never revive an exited/replaced
    // mode from a late capture callback. Legacy enter-picker remains one-shot.
    if (message.type === 'selection' && persistentPicker && !page.closed &&
        pickerGeneration === page.pickerGeneration && page.persistentPicker) {
      const nextLabel = message.payload.delivery === 'queue' ? (persistentPicker.label ?? 1) + 1 : persistentPicker.label ?? 1
      await this.message(page.tabId, { v: 1, ...persistentPicker,
        mode: message.payload.delivery === 'queue' ? 'batch' : persistentPicker.mode,
        label: Math.min(99, nextLabel),
      }).catch(error => {
        if (!page.closed) console.error('Failed to continue browser annotation', error)
      })
    }
  }

  private async withNativeSelectionScreenshot(
    page: WorkspaceBrowserPage,
    message: Extract<PreviewAgentMessage, { type: 'selection' }>,
  ): Promise<PreviewAgentMessage> {
    const navigationId = page.navigationId
    const screenshot = isPlainRecord(message.payload.screenshot) ? message.payload.screenshot : {}
    const captureId = typeof screenshot.captureId === 'number' ? screenshot.captureId : undefined
    try {
      return {
        ...message,
        payload: {
          ...message.payload,
          screenshot: {
            ...screenshot,
            kind: screenshot.kind ?? 'region',
            dataUrl: await this.captureDataUrl(page, 'viewport'),
          },
        },
      }
    } catch {
      return message
    } finally {
      if (navigationId === page.navigationId) await this.clearSelectionOverlay(page, captureId)
    }
  }

  private async clearSelectionOverlay(page: WorkspaceBrowserPage, captureId?: number): Promise<void> {
    const webContents = page.webContents
    if (page.closed || webContents.isDestroyed?.()) return
    try {
      await webContents.executeJavaScript(`globalThis.__PREVIEW_AGENT_CLEAR_SELECTION_OVERLAY__?.(${captureId === undefined ? '' : JSON.stringify(captureId)})`)
    } catch {
      // The page may navigate while the native capture is in flight.
    }
  }

  private async injectPreviewAgent(page: WorkspaceBrowserPage): Promise<void> {
    const webContents = page.webContents
    if (page.closed || webContents.isDestroyed?.()) return
    page.pickerArmed = false
    page.persistentPicker = null
    page.pickerGeneration += 1
    const navigationId = page.navigationId
    const script = readFileSync(resolvePreviewScriptPath(this.previewScriptPath), 'utf8')
    await webContents.executeJavaScript(script)
    if (navigationId !== page.navigationId) return
    page.controlsSignature = null
    await this.syncBrowserControls(page)
  }

  private async syncBrowserControls(page: WorkspaceBrowserPage): Promise<void> {
    if (!page.controls || page.closed || page.webContents.isDestroyed?.()) return
    const raw = JSON.stringify({ ...page.controls, zoomFactor: page.zoomFactor })
    if (page.controlsSignature === raw) return
    page.controlsSignature = raw
    try {
      await page.webContents.executeJavaScript(`globalThis.__PREVIEW_BRIDGE__?.handleHostRaw(${JSON.stringify(raw)})`)
    } catch (error) {
      if (page.controlsSignature === raw) page.controlsSignature = null
      throw error
    }
  }

  private assertCaptureCurrent(page: WorkspaceBrowserPage, navigationId: number, generation: number): void {
    if (page.closed || this.pages.get(page.tabId) !== page || page.webContents.isDestroyed?.() ||
      page.parent !== this.parent || page.parent.isDestroyed?.() || page.parent.webContents?.isDestroyed?.() ||
      page.navigationId !== navigationId || page.captureGeneration !== generation) {
      throw new Error('browser capture superseded by a page or window change')
    }
    if (this.unsafeCaptureParents.has(page.parent)) throw new Error('browser capture restoration was not confirmed')
  }

  private async withCaptureGeometry<T>(page: WorkspaceBrowserPage, exclusive: boolean, task: () => Promise<T>): Promise<T> {
    let gate = this.captureGates.get(page.parent)
    if (!gate) {
      gate = new WorkspaceBrowserCaptureGate()
      this.captureGates.set(page.parent, gate)
    }
    const release = await gate.acquire(exclusive)
    try {
      if (this.unsafeCaptureParents.has(page.parent)) throw new Error('browser capture restoration was not confirmed')
      return await task()
    } finally {
      release()
    }
  }

  private async withHiddenCaptureChrome(page: WorkspaceBrowserPage, task: () => Promise<string>): Promise<string> {
    const navigationId = page.navigationId
    const webContents = page.webContents
    const hideChrome = async (hidden: boolean) => {
      if (page.closed || webContents.isDestroyed?.() || page.navigationId !== navigationId) return
      await webContents.executeJavaScript(`globalThis.__PREVIEW_AGENT_SET_CHROME_HIDDEN__?.(${hidden})`)
    }
    page.captureCount += 1
    try {
      await hideChrome(true)
      return await task()
    } finally {
      page.captureCount -= 1
      if (page.captureCount === 0) {
        try { await hideChrome(false) } catch { /* The original document may be gone. */ }
      }
    }
  }

  private async captureDataUrl(page: WorkspaceBrowserPage, kind: WorkspaceBrowserCaptureKind): Promise<string> {
    if (kind === 'full') return await this.captureFullPageDataUrl(page)
    const navigationId = page.navigationId
    const generation = page.captureGeneration
    let expired = false
    return await this.withHiddenCaptureChrome(page, async () => {
      // The UI timeout is not cancellation of Electron's capturePage. Keep the
      // read lease until that original native promise settles, so a full-page
      // writer cannot resize its host underneath a late native capture.
      const pending = this.withCaptureGeometry(page, false, async () => {
        this.assertCaptureCurrent(page, navigationId, generation)
        if (expired) throw new Error('browser capture timed out before it acquired geometry')
        if (!page.webContents.capturePage) throw new Error('native browser capture unavailable')
        const image = await page.webContents.capturePage()
        this.assertCaptureCurrent(page, navigationId, generation)
        if (expired) throw new Error('browser capture timed out')
        return image.toDataURL()
      })
      try {
        return await withTimeout(pending, this.captureTimeoutMs, 'browser capture timed out')
      } catch (error) {
        expired = true
        throw error
      }
    })
  }

  private async captureFullPageDataUrl(page: WorkspaceBrowserPage): Promise<string> {
    if (page.fullCapture) return await page.fullCapture
    const promise = this.captureFullPageDataUrlOnce(page)
    page.fullCapture = promise
    try {
      return await promise
    } finally {
      if (page.fullCapture === promise) page.fullCapture = null
    }
  }

  private async captureFullPageDataUrlOnce(page: WorkspaceBrowserPage): Promise<string> {
    const navigationId = page.navigationId
    const generation = page.captureGeneration
    return await this.withCaptureGeometry(page, true, async () => {
      this.assertCaptureCurrent(page, navigationId, generation)
      return await this.withHiddenCaptureChrome(page, async () => {
        const parent = page.parent
        const host = parent.webContents
        const guest = page.webContents
        const parentDebugger = host?.debugger
        const guestDebugger = guest.debugger
        if (!host?.executeJavaScript || !host.getZoomFactor || !host.isDevToolsOpened || !guest.isDevToolsOpened ||
          !parent.getContentBounds || !parent.on || !parent.removeListener ||
          !parentDebugger?.on || !parentDebugger.removeListener || !guestDebugger?.on || !guestDebugger.removeListener) {
          throw new Error('full browser capture owner unavailable')
        }
        // Read-only guest capture used to borrow an existing debugger. This path
        // changes the host's size and must never clear someone else's emulation.
        if (parentDebugger.isAttached() || guestDebugger.isAttached() || host.isDevToolsOpened() || guest.isDevToolsOpened()) {
          throw new Error('full browser capture debugger is busy')
        }
        const token = randomUUID()
        let hostAttached = false
        let guestAttached = false
        let hostDetached = false
        let guestDetached = false
        let windowChanged = false
        let styleAttempted = false
        let hostSizeAttempted = false
        let guestTokenAttempted = false
        let documentChanged = false
        let original: FullCaptureSample | undefined
        let dataUrl: string | undefined
        let primaryError: unknown
        const cleanupErrors: unknown[] = []
        const hostDetach = () => { hostDetached = true }
        const guestDetach = () => { guestDetached = true }
        const windowChange = () => { windowChanged = true }
        // Promise.race is not cancellation of CDP or renderer mutation. Keep
        // this writer until the actual commands settle, as the original full
        // capture did; only read-only geometry sampling has a timeout.
        const command = (api: WorkspaceBrowserDebuggerLike, method: string, params?: Record<string, unknown>) =>
          api.sendCommand(method, params)
        const hostOperation = async (action: FullCaptureHostRequest['action'], width?: number, height?: number) => {
          const operation = host.executeJavaScript!(scriptForFullCaptureHost({ action, webContentsId: page.webContentsId, token, width, height }))
          return action === 'snapshot' ? await withTimeout(operation, this.captureTimeoutMs, 'browser capture host inspection timed out') : await operation
        }
        const guestOperation = (action: FullCaptureGuestRequest['action'], x?: number, y?: number) =>
          guest.executeJavaScript(scriptForFullCaptureGuest({ action, token, x, y }))
        const zooms = { host: host.getZoomFactor(), guest: guest.getZoomFactor?.() }
        const initialBounds = parent.getContentBounds()
        const current = () => {
          this.assertCaptureCurrent(page, navigationId, generation)
          if (windowChanged || hostDetached || guestDetached || !parentDebugger.isAttached() || !guestDebugger.isAttached() ||
            host.isDevToolsOpened!() || guest.isDevToolsOpened!() || host.getZoomFactor!() !== zooms.host ||
            guest.getZoomFactor?.() !== zooms.guest || !sameCaptureBounds(initialBounds, parent.getContentBounds!())) {
            throw new Error('full browser capture owner or geometry changed')
          }
        }
        const sample = async (): Promise<FullCaptureSample> => {
          const [hostValue, guestValue, metricsValue] = await Promise.all([
            hostOperation('snapshot'),
            withTimeout(guestOperation('snapshot'), this.captureTimeoutMs, 'browser capture guest inspection timed out'),
            withTimeout(command(guestDebugger, 'Page.getLayoutMetrics'), this.captureTimeoutMs, 'browser capture layout metrics timed out'),
          ])
          return { host: parseFullCaptureHost(hostValue), guest: parseFullCaptureGuest(guestValue), metrics: parseFullCaptureMetrics(metricsValue) }
        }
        const stable = async (accept: (value: FullCaptureSample) => boolean, label: string): Promise<FullCaptureSample> => {
          const deadline = Date.now() + this.captureTimeoutMs
          let previous = ''
          let count = 0
          do {
            const value = await sample()
            if (accept(value)) {
              const signature = JSON.stringify(value)
              count = signature === previous ? count + 1 : 1
              previous = signature
              if (count === 3) return value
            } else {
              previous = ''
              count = 0
            }
            await new Promise(resolve => setTimeout(resolve, 16))
          } while (Date.now() < deadline)
          throw new Error(`full browser capture layout did not stabilize: ${label}`)
        }
        try {
          parentDebugger.on('detach', hostDetach)
          guestDebugger.on('detach', guestDetach)
          parent.on('resize', windowChange)
          parent.on('move', windowChange)
          parentDebugger.attach('1.3')
          hostAttached = true
          guestDebugger.attach('1.3')
          guestAttached = true
          await hostOperation('begin')
          guestTokenAttempted = true
          await guestOperation('begin')
          original = await stable(() => { current(); return true }, 'original')
          const plan = planFullCapture(original, zooms.host, zooms.guest, initialBounds)
          styleAttempted = true // Restore even if the renderer applies CSS but its reply is lost.
          await hostOperation('resize', original.host.element.width, plan.elementHeight)
          await stable(value => {
            current()
            return fullCaptureHeightReady(original!, value, plan)
          }, 'expanded height')
          await hostOperation('resize', plan.elementWidth, plan.elementHeight)
          const expanded = await stable(value => {
            current()
            return fullCaptureLayoutPreserved(original!, value, plan)
          }, 'expanded guest')
          hostSizeAttempted = true // The clear path must run even if setVisibleSize rejects after dispatch.
          await command(parentDebugger, 'Emulation.setVisibleSize', { width: plan.visibleWidth, height: plan.visibleHeight })
          const prepared = await stable(value => {
            current()
            return fullCaptureLayoutPreserved(original!, value, plan) &&
              nearlyEqual(value.host.height, plan.visibleHeight / zooms.host, 1) &&
              sameCaptureGeometry(expanded.guest, value.guest) && sameCaptureMetrics(expanded.metrics, value.metrics)
          }, 'host compositor')
          current()
          const screenshot = await command(guestDebugger, 'Page.captureScreenshot', {
            format: 'png', fromSurface: true, captureBeyondViewport: true,
            clip: { x: 0, y: 0, width: plan.clipWidth, height: plan.clipHeight, scale: 1 },
          })
          current()
          if (!isPlainRecord(screenshot) || typeof screenshot.data !== 'string' || !screenshot.data) {
            throw new Error('invalid full capture screenshot data')
          }
          const after = await sample()
          current()
          if (!sameCaptureGeometry(prepared.guest, after.guest) || !sameCaptureMetrics(prepared.metrics, after.metrics) ||
            !sameCaptureGeometry(prepared.host, after.host)) throw new Error('full browser capture layout changed while painting')
          dataUrl = `data:image/png;base64,${screenshot.data}`
        } catch (error) {
          primaryError = error
        } finally {
          const attempt = async (operation: () => Promise<unknown> | unknown) => {
            try { await operation() } catch (error) { cleanupErrors.push(error) }
          }
          if (hostSizeAttempted) {
            // Do NOT replay the old setVisibleSize: ClearDeviceEmulationSize
            // preserves a real intervening native resize when current != emulated.
            // Enabling only the clear path skips a new size write entirely.
            await attempt(async () => {
              if (hostDetached || !parentDebugger.isAttached()) throw new Error('host debugger ownership lost before restoration')
              await command(parentDebugger, 'Emulation.setDeviceMetricsOverride', {
                width: 0, height: 0, deviceScaleFactor: 0, mobile: false, dontSetVisibleSize: true,
              })
            })
            await attempt(async () => {
              if (hostDetached || !parentDebugger.isAttached()) throw new Error('host debugger ownership lost before clear')
              await command(parentDebugger, 'Emulation.clearDeviceMetricsOverride')
            })
          }
          if (styleAttempted) await attempt(() => hostOperation('restore'))
          let sameDocument = !page.closed && !guest.isDestroyed?.() && page.navigationId === navigationId
          if (original && sameDocument) {
            await attempt(async () => {
              const restored = await guestOperation('restore', original!.guest.scrollX, original!.guest.scrollY)
              if (!isPlainRecord(restored) || typeof restored.documentCurrent !== 'boolean') throw new Error('invalid capture scroll restoration result')
              if (!restored.documentCurrent) {
                sameDocument = false
                documentChanged = true
              }
            })
          }
          if (styleAttempted || hostSizeAttempted) {
            await attempt(async () => {
              // A resize makes the image stale, but cleanup must retain the *new*
              // native size. Check current native bounds against current CSS zoom.
              const deadline = Date.now() + this.captureTimeoutMs
              let stableCount = 0
              let previous = ''
              do {
                const value = parseFullCaptureHost(await hostOperation('snapshot'))
                const bounds = parent.getContentBounds!()
                const zoom = host.getZoomFactor!()
                let restored = nearlyEqual(value.width * zoom, bounds.width, zoom) && nearlyEqual(value.height * zoom, bounds.height, zoom)
                if (restored && original && sameDocument && !windowChanged && !hostDetached && !guestDetached && generation === page.captureGeneration) {
                  const complete = await sample()
                  restored = sameCaptureGeometry(original.host, complete.host) && sameCaptureGeometry(original.guest, complete.guest) &&
                    sameCaptureMetrics(original.metrics, complete.metrics)
                }
                const signature = JSON.stringify({ value, bounds, zoom })
                stableCount = restored ? (signature === previous ? stableCount + 1 : 1) : 0
                previous = signature
                if (stableCount === 3) return
                await new Promise(resolve => setTimeout(resolve, 16))
              } while (Date.now() < deadline)
              throw new Error('full browser capture native size restoration did not stabilize')
            })
          }
          if (guestTokenAttempted && !guest.isDestroyed?.()) await attempt(() => guestOperation('release'))
          await attempt(() => hostOperation('release'))
          parent.removeListener('resize', windowChange)
          parent.removeListener('move', windowChange)
          parentDebugger.removeListener('detach', hostDetach)
          guestDebugger.removeListener('detach', guestDetach)
          if (guestAttached && !guestDetached) await attempt(() => { if (guestDebugger.isAttached()) guestDebugger.detach() })
          if (hostAttached && !hostDetached) await attempt(() => { if (parentDebugger.isAttached()) parentDebugger.detach() })
          if (cleanupErrors.length > 0 && hostSizeAttempted) this.unsafeCaptureParents.add(parent)
        }
        if (cleanupErrors.length > 0) throw new AggregateError(primaryError === undefined ? cleanupErrors : [primaryError, ...cleanupErrors], 'browser capture restoration failed')
        if (primaryError !== undefined) throw primaryError
        this.assertCaptureCurrent(page, navigationId, generation)
        if (documentChanged || windowChanged || host.getZoomFactor() !== zooms.host || guest.getZoomFactor?.() !== zooms.guest) {
          throw new Error('full browser capture superseded during restoration')
        }
        if (!dataUrl) throw new Error('full browser capture produced no image')
        return dataUrl
      })
    })
  }

  /**
   * Put a page back at the zoom Chromium would have given it as a native
   * page: whatever its host was last zoomed to, otherwise 100%. Electron
   * instead resets a guest to its embedder's zoom, i.e. the app zoom.
   */
  private restoreNavigationZoom(page: WorkspaceBrowserPage): void {
    const webContents = page.webContents
    if (page.closed || webContents.isDestroyed?.() || !webContents.setZoomFactor) return
    const wanted = this.zoomByHost.get(zoomHostKey(webContents.getURL())) ?? 1
    const actual = webContents.getZoomFactor?.()
    // Factors round-trip through zoom levels, so compare with a tolerance.
    if (actual !== undefined && Math.abs(actual - wanted) < 0.001) return
    webContents.setZoomFactor(wanted)
    this.emitState(page)
  }

  private recordVisit(page: WorkspaceBrowserPage, url: string): void {
    // A visit log, not a back/forward stack: Electron's navigation entries carry
    // no timestamps, and the native history stays the source of truth for
    // `goBack`/`goForward` and for the `canGo*` flags on every state event.
    if (!isHttpUrl(url)) return
    const last = page.history[page.history.length - 1]
    if (last?.url === url) return
    page.history.push({ url, title: page.webContents.getTitle(), visitedAt: Date.now() })
    if (page.history.length > MAX_HISTORY_ENTRIES) page.history.shift()
    this.emitFor(page, { type: 'history', tabId: page.tabId, entries: [...page.history] })
  }

  private emitState(page: WorkspaceBrowserPage): void {
    const webContents = page.webContents
    if (webContents.isDestroyed?.()) return
    const actualZoom = webContents.getZoomFactor?.()
    if (actualZoom !== undefined && Number.isFinite(actualZoom) && actualZoom > 0) page.zoomFactor = actualZoom
    void this.syncBrowserControls(page).catch(error => {
      if (!page.closed) console.error('Failed to update workspace browser controls', error)
    })
    // Until its first real navigation the guest shows the blank document it
    // attached with. A tab without a page has no address and no title.
    const blank = webContents.getURL() === WORKSPACE_BROWSER_INITIAL_SRC
    this.emitFor(page, {
      type: 'state',
      tabId: page.tabId,
      url: blank ? '' : webContents.getURL(),
      title: blank ? '' : webContents.getTitle(),
      canGoBack: readCanGoBack(webContents),
      canGoForward: readCanGoForward(webContents),
      loading: webContents.isLoading(),
      navigationId: page.navigationId,
      navigationOutcome: page.navigationOutcome,
      zoomFactor: page.zoomFactor,
      annotationActive: page.persistentPicker !== null,
    })
  }

  /**
   * Exit for page-scoped events. A native callback that fires after `close` —
   * a queued `did-stop-loading` or a crash notice — is
   * dropped here instead of being applied to whatever took the tab's slot.
   */
  private emitFor(page: WorkspaceBrowserPage, event: WorkspaceBrowserEvent): void {
    if (page.closed) return
    this.emit(event)
  }

  private findPageByWebContents(webContents: unknown): WorkspaceBrowserPage | null {
    if (!webContents) return null
    for (const page of this.pages.values()) {
      if (page.webContents === webContents) return page
    }
    return null
  }

  private nextDownloadId(): string {
    this.downloadSequence += 1
    return `wbd-${this.downloadSequence}`
  }

  private requirePage(tabId: string): WorkspaceBrowserPage {
    const page = this.pages.get(tabId)
    if (!page) throw new Error(`workspace browser tab not open: ${tabId}`)
    return page
  }
}

/**
 * Chromium keys page zoom by host name alone — port and scheme do not count —
 * or by the whole URL when there is no host (`net::GetHostOrSpecFromURL`).
 */
function zoomHostKey(url: string): string {
  try {
    const { hostname } = new URL(url)
    return hostname ? hostname.replace(/\.$/, '') : url
  } catch {
    return url
  }
}

function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  return Promise.race([
    promise,
    new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(message)), ms)
    }),
  ]).finally(() => clearTimeout(timer))
}

function readCanGoBack(webContents: WorkspaceBrowserWebContentsLike): boolean {
  return webContents.navigationHistory?.canGoBack() ?? webContents.canGoBack?.() ?? false
}

function readCanGoForward(webContents: WorkspaceBrowserWebContentsLike): boolean {
  return webContents.navigationHistory?.canGoForward() ?? webContents.canGoForward?.() ?? false
}

function normalizeDownloadState(state: string): 'progressing' | 'completed' | 'cancelled' | 'interrupted' {
  if (state === 'completed' || state === 'cancelled' || state === 'interrupted') return state
  return 'progressing'
}
