import fs from 'node:fs'
import { createRequire } from 'node:module'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { WorkspaceBrowserEvent } from '../../src/lib/desktopHost/types'
import {
  ElectronWorkspaceBrowserService,
  type WorkspaceBrowserWebContentsLike,
} from './workspaceBrowser'

type Handler = (...args: never[]) => void
type CaptureImage = { toDataURL(): string }
type LayoutState = {
  width: number, height: number, clientWidth: number, clientHeight: number
  scrollWidth: number, scrollHeight: number, physicalHeight: number
  bodyWidth: number, bodyHeight: number, gutter: number
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

const cleanups: Array<() => void> = []
const { JSDOM } = createRequire(import.meta.url)('jsdom') as {
  JSDOM: new (html: string, options: { runScripts: 'outside-only' }) => { window: Window & typeof globalThis }
}

async function assertEventually(assertion: () => void, timeoutMs = 1_000): Promise<void> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    try {
      assertion()
      return
    } catch (error) {
      const remaining = deadline - Date.now()
      if (remaining <= 0) throw error
      await new Promise(resolve => setTimeout(resolve, Math.min(10, remaining)))
    }
  }
}

function isolatedWindow() {
  const { window } = new JSDOM('<!doctype html><html><body></body></html>', { runScripts: 'outside-only' })
  cleanups.push(() => window.close())
  for (const [key, value] of Object.entries({ width: 2560, height: 1440, availWidth: 2560, availHeight: 1353, colorDepth: 30, pixelDepth: 30 })) {
    Object.defineProperty(window.screen, key, { configurable: true, value })
  }
  return { window, document: window.document, run: (script: string) => window.eval(script) as unknown }
}

class CaptureDebugger {
  attached = false
  devToolsOpened = false
  commands: Array<{ method: string, params?: Record<string, unknown> }> = []
  handlers = new Map<string, Handler[]>()
  attach = vi.fn(() => { this.attached = true })
  detach = vi.fn(() => { this.attached = false })
  isAttached = () => this.attached
  reply: (method: string, params?: Record<string, unknown>) => Promise<unknown> = async () => ({})
  sendCommand = vi.fn(async (method: string, params?: Record<string, unknown>) => {
    this.commands.push({ method, params })
    return await this.reply(method, params)
  })
  on(event: string, handler: Handler) {
    this.handlers.set(event, [...(this.handlers.get(event) ?? []), handler])
    return this
  }
  removeListener(event: string, handler: Handler) {
    this.handlers.set(event, (this.handlers.get(event) ?? []).filter(value => value !== handler))
    return this
  }
}

class CaptureGuest implements WorkspaceBrowserWebContentsLike {
  realm = isolatedWindow()
  readonly debugger = new CaptureDebugger()
  readonly scripts: string[] = []
  readonly handlers = new Map<string, Handler[]>()
  readonly contentSize = { x: 0, y: 0, width: 585, height: 1333 }
  readonly captureResult = deferred<unknown>()
  destroyed = false
  zoom = 1
  url = 'about:blank'
  scroll = { x: 17, y: 83 }
  scrollCalls = 0
  scrollRestoreGate: ReturnType<typeof deferred<void>> | undefined
  nextEvaluationGate: { entered: ReturnType<typeof deferred<void>>, release: ReturnType<typeof deferred<void>> } | undefined
  layoutHysteresis = false
  layoutHistory: LayoutState[] = []
  private verticalScrollbar = true
  measure = () => ({ width: 600, height: 600 })
  capturePage = vi.fn(async (): Promise<CaptureImage> => ({ toDataURL: () => 'data:image/png;base64,VIEWPORT' }))
  beforeScreenshot: () => void = () => {}

  constructor(readonly id: number) {
    Object.defineProperties(this.realm.window, {
      scrollX: { configurable: true, get: () => this.scroll.x },
      scrollY: { configurable: true, get: () => this.scroll.y },
      devicePixelRatio: { configurable: true, get: () => 2 },
      innerWidth: { configurable: true, get: () => this.measure().width },
      innerHeight: { configurable: true, get: () => this.measure().height },
    })
    Object.defineProperties(this.realm.document.documentElement, {
      clientWidth: { configurable: true, get: () => this.layoutState().clientWidth },
      clientHeight: { configurable: true, get: () => this.layoutState().clientHeight },
      scrollWidth: { configurable: true, get: () => this.layoutState().scrollWidth },
      scrollHeight: { configurable: true, get: () => this.layoutState().scrollHeight },
    })
    Object.defineProperties(this.realm.document.body, {
      scrollWidth: { configurable: true, get: () => this.layoutState().bodyWidth },
      scrollHeight: { configurable: true, get: () => Math.floor(this.layoutState().bodyHeight) },
    })
    this.realm.window.scrollTo = ((input: number | ScrollToOptions, y?: number) => {
      this.scrollCalls += 1
      this.scroll = typeof input === 'number'
        ? { x: input, y: y ?? this.scroll.y }
        : { x: input.left ?? this.scroll.x, y: input.top ?? this.scroll.y }
    }) as typeof window.scrollTo
    const main = this.realm.document.createElement('main')
    main.innerHTML = '<h1>Capture fixture</h1><p>Stable content column</p><footer>End of document</footer>'
    this.realm.document.body.append(main)
    for (const node of [this.realm.document.body, main]) {
      node.getBoundingClientRect = () => {
        const { bodyWidth, bodyHeight } = this.layoutState()
        return {
          x: 24 - this.scroll.x, y: 24 - this.scroll.y,
          left: 24 - this.scroll.x, top: 24 - this.scroll.y,
          right: 24 + bodyWidth - this.scroll.x, bottom: 24 + bodyHeight - this.scroll.y,
          width: bodyWidth, height: bodyHeight, toJSON: () => ({}),
        }
      }
    }
    this.debugger.reply = async method => {
      if (method === 'Page.getLayoutMetrics') {
        const state = this.layoutState()
        const cssViewport = { pageX: this.scroll.x, pageY: this.scroll.y, clientWidth: state.clientWidth, clientHeight: state.clientHeight }
        const physicalViewport = { pageX: this.scroll.x * 2, pageY: this.scroll.y * 2, clientWidth: state.clientWidth * 2, clientHeight: state.clientHeight * 2 }
        // Electron 42 can round the CSS document height down while the physical
        // document remains 2667px: dividing by DPR requires a 1334px surface.
        return {
          contentSize: { x: 0, y: 0, width: state.scrollWidth * 2, height: state.physicalHeight },
          cssContentSize: { x: 0, y: 0, width: state.scrollWidth, height: state.scrollHeight },
          layoutViewport: physicalViewport,
          visualViewport: { ...physicalViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: this.zoom },
          cssLayoutViewport: cssViewport,
          cssVisualViewport: { ...cssViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: this.zoom },
        }
      }
      if (method === 'Page.captureScreenshot') {
        this.beforeScreenshot()
        return await this.captureResult.promise
      }
      return {}
    }
  }

  layoutState(): LayoutState {
    const { width, height } = this.measure()
    // Resolve layout using the previous scrollbar state, as the native
    // regression did. Narrowing first can wrap one line and retain the gutter.
    const availableWidth = width - (this.verticalScrollbar ? 15 : 0)
    let wrapped = this.layoutHysteresis && availableWidth < 578
    const initialBodyHeight = 1285.4375 + (wrapped ? 37 : 0)
    this.verticalScrollbar = initialBodyHeight + 48 > height
    const clientWidth = width - (this.verticalScrollbar ? 15 : 0)
    wrapped = this.layoutHysteresis && clientWidth < 578
    const bodyHeight = 1285.4375 + (wrapped ? 37 : 0)
    const state = {
      width, height, clientWidth, clientHeight: height,
      scrollWidth: clientWidth, scrollHeight: Math.max(Math.floor(bodyHeight + 48), height),
      physicalHeight: Math.max(Math.ceil((bodyHeight + 48) * 2), height * 2),
      bodyWidth: clientWidth - 48, bodyHeight, gutter: width - clientWidth,
    }
    if (JSON.stringify(state) !== JSON.stringify(this.layoutHistory.at(-1))) this.layoutHistory.push(state)
    return state
  }

  async loadURL(url: string) { this.url = url }
  getURL() { return this.url }
  getTitle() { return 'Fixture' }
  isLoading() { return false }
  reload() {}
  reloadIgnoringCache() {}
  stop() {}
  findInPage() { return 1 }
  stopFindInPage() {}
  setWindowOpenHandler() {}
  getZoomFactor() { return this.zoom }
  setZoomFactor(factor: number) { this.zoom = factor }
  isDevToolsOpened() { return this.debugger.devToolsOpened }
  isDestroyed() { return this.destroyed }
  close() {
    this.destroyed = true
    this.emit('destroyed')
  }
  async executeJavaScript(script: string) {
    this.scripts.push(script)
    const evaluation = this.nextEvaluationGate
    if (evaluation) {
      this.nextEvaluationGate = undefined
      evaluation.entered.resolve()
      await evaluation.release.promise
    }
    const scrollCalls = this.scrollCalls
    const result = await this.realm.run(script)
    // Model a real Electron execution whose acknowledgement is delayed after
    // its scroll side effect, without identifying the script by source text.
    if (this.scrollCalls !== scrollCalls && this.scrollRestoreGate) await this.scrollRestoreGate.promise
    return result
  }
  replaceDocument(scroll: { x: number, y: number }) {
    this.realm = isolatedWindow()
    this.scroll = { ...scroll }
    this.realm.document.body.innerHTML = '<main>Replacement document must keep its scroll</main>'
    Object.defineProperties(this.realm.window, {
      scrollX: { configurable: true, get: () => this.scroll.x },
      scrollY: { configurable: true, get: () => this.scroll.y },
    })
    this.realm.window.scrollTo = ((input: number | ScrollToOptions, y?: number) => {
      this.scrollCalls += 1
      this.scroll = typeof input === 'number'
        ? { x: input, y: y ?? this.scroll.y }
        : { x: input.left ?? this.scroll.x, y: input.top ?? this.scroll.y }
    }) as typeof window.scrollTo
  }
  on(event: string, handler: Handler) {
    this.handlers.set(event, [...(this.handlers.get(event) ?? []), handler])
    return this
  }
  emit(event: string, ...args: unknown[]) {
    for (const handler of this.handlers.get(event) ?? []) (handler as (...values: unknown[]) => void)(...args)
  }
}

function captureHarness(options: { captureTimeoutMs?: number } = {}) {
  const host = isolatedWindow()
  const hostDebugger = new CaptureDebugger()
  const bounds = { x: 30, y: 40, width: 600, height: 728 }
  let visibleSize = { width: bounds.width, height: bounds.height }
  const handlers = new Map<string, Handler[]>()
  const executeJavaScript = vi.fn(async (script: string) => await host.run(script))
  const parent = {
    getContentBounds: () => ({ ...bounds }),
    isDestroyed: () => false,
    on: (event: string, handler: Handler) => {
      handlers.set(event, [...(handlers.get(event) ?? []), handler])
    },
    removeListener: (event: string, handler: Handler) => {
      handlers.set(event, (handlers.get(event) ?? []).filter(value => value !== handler))
    },
    webContents: {
      executeJavaScript,
      getZoomFactor: () => 1,
      isDevToolsOpened: () => hostDebugger.devToolsOpened,
      isDestroyed: () => false,
      debugger: hostDebugger,
    },
  }
  Object.defineProperties(host.window, {
    innerWidth: { configurable: true, get: () => visibleSize.width },
    innerHeight: { configurable: true, get: () => visibleSize.height },
    devicePixelRatio: { configurable: true, get: () => 2 },
  })
  for (const node of [host.document.documentElement, host.document.body]) {
    Object.defineProperties(node, {
      clientWidth: { configurable: true, get: () => visibleSize.width },
      clientHeight: { configurable: true, get: () => visibleSize.height },
      scrollWidth: { configurable: true, get: () => visibleSize.width },
      scrollHeight: { configurable: true, get: () => visibleSize.height },
    })
  }
  hostDebugger.reply = async (method, params) => {
    if (method === 'Emulation.setVisibleSize') visibleSize = { width: Number(params?.width), height: Number(params?.height) }
    if (method === 'Emulation.clearDeviceMetricsOverride') visibleSize = { width: bounds.width, height: bounds.height }
    if (method === 'Page.getLayoutMetrics') {
      const cssViewport = { clientWidth: visibleSize.width, clientHeight: visibleSize.height, pageX: 0, pageY: 0 }
      const physicalViewport = { clientWidth: visibleSize.width * 2, clientHeight: visibleSize.height * 2, pageX: 0, pageY: 0 }
      return {
        contentSize: { x: 0, y: 0, width: visibleSize.width * 2, height: visibleSize.height * 2 },
        cssContentSize: { x: 0, y: 0, width: visibleSize.width, height: visibleSize.height },
        layoutViewport: physicalViewport,
        visualViewport: { ...physicalViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: 1 },
        cssLayoutViewport: cssViewport,
        cssVisualViewport: { ...cssViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: 1 },
      }
    }
    return {}
  }
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'workspace-full-capture-'))
  cleanups.push(() => fs.rmSync(directory, { recursive: true, force: true }))
  const previewScriptPath = path.join(directory, 'preview-agent.js')
  fs.writeFileSync(previewScriptPath, 'void 0')
  const events: WorkspaceBrowserEvent[] = []
  const guests: CaptureGuest[] = []
  const elements: HTMLElement[] = []
  const layer = host.document.createElement('div')
  layer.dataset.testid = 'workspace-browser-guest-layer'
  layer.className = 'pointer-events-none absolute inset-0 z-0'
  host.document.body.append(layer)
  const service = new ElectronWorkspaceBrowserService({
    previewScriptPath,
    captureTimeoutMs: options.captureTimeoutMs,
    emit: event => events.push(event),
    resolveGuest: id => {
      const guest = guests.find(value => value.id === id && !value.destroyed)
      if (!guest) throw new Error('fixture guest missing')
      return guest
    },
  })
  const open = async (tabId: string) => {
    const guest = new CaptureGuest(100 + guests.length)
    cleanups.push(() => guest.captureResult.resolve({ data: 'CLEANUP' }))
    guests.push(guest)
    // Match browserGuests.ts: every guest stays in its permanent clipped slot.
    // The service may resize only the webview, never move it or rewrite its slot.
    const slot = host.document.createElement('div')
    slot.dataset.workspaceBrowserSlot = tabId
    slot.dataset.shown = 'true'
    Object.assign(slot.style, { position: 'absolute', left: '10px', top: '90px',
      width: '600px', height: '600px', overflow: 'hidden', visibility: 'visible', pointerEvents: 'auto' })
    const element = host.document.createElement('webview')
    element.setAttribute('data-tab-id', tabId)
    // Install the priority through the CSS parser. The bundled JSDOM CSSOM
    // leaves getAttribute stale when only the priority of an equal value changes.
    element.setAttribute('style', 'display: flex; width: 100% !important; height: 100%; border: 0px; opacity: 0.75;')
    Object.assign(element, { getWebContentsId: () => guest.id })
    guest.measure = () => ({
      width: element.style.width.endsWith('px') ? Number.parseFloat(element.style.width) : 600,
      height: element.style.height.endsWith('px') ? Number.parseFloat(element.style.height) : 600,
    })
    element.getBoundingClientRect = () => {
      const { width, height } = guest.measure()
      return { x: 10, y: 90, left: 10, top: 90, right: 10 + width, bottom: 90 + height, width, height, toJSON: () => ({}) }
    }
    slot.append(element)
    layer.append(slot)
    elements.push(element)
    await service.create(parent, tabId, { storageId: tabId, webContentsId: guest.id })
    return { guest, element, slot }
  }
  return {
    service, host, hostDebugger, parent, bounds, events, guests, elements, open,
    get visibleSize() { return visibleSize },
    resize(width: number, height: number) {
      bounds.width = width
      bounds.height = height
      for (const handler of handlers.get('resize') ?? []) (handler as () => void)()
    },
  }
}

const screenshots = (events: WorkspaceBrowserEvent[]) => events.filter(event => event.type === 'screenshot')
const screenshotCommands = (guest: CaptureGuest) => guest.debugger.commands.filter(command => command.method === 'Page.captureScreenshot')
const flush = () => new Promise<void>(resolve => setImmediate(resolve))

afterEach(() => {
  for (const cleanup of cleanups.splice(0).reverse()) cleanup()
})

describe('workspace browser full capture transaction', () => {
  it('captures only the registered guest with enough host surface and restores exact sizing and scroll', async () => {
    const h = captureHarness()
    const a = await h.open('a')
    const b = await h.open('b')
    // Tab attributes can be stale or reused: guest identity is authoritative.
    a.element.setAttribute('data-tab-id', 'b')
    b.element.setAttribute('data-tab-id', 'a')
    const originalStyle = a.element.getAttribute('style')
    const otherStyle = b.element.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const originalBounds = { ...h.bounds }
    const originalSlotStyle = a.slot.getAttribute('style')
    let duringCapture: unknown
    a.guest.beforeScreenshot = () => {
      duringCapture = {
        width: a.element.style.width,
        height: a.element.style.height,
        bottom: a.element.getBoundingClientRect().bottom,
        otherStyle: b.element.getAttribute('style'),
        visibleHeight: h.visibleSize.height,
        bounds: { ...h.bounds },
        slotStyle: a.slot.getAttribute('style'),
      }
      // A concurrent renderer style update must survive capture cleanup.
      a.element.style.setProperty('outline-color', 'red')
    }
    const pending = h.service.capture('a', 'full')
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    a.guest.captureResult.resolve({ data: 'FULL' })
    await pending
    expect(duringCapture).toEqual({ width: '585px', height: '1334px', bottom: 1424, otherStyle,
      visibleHeight: expect.any(Number), bounds: originalBounds, slotStyle: originalSlotStyle })
    const observed = duringCapture as { visibleHeight: number, bottom: number }
    expect(observed.visibleHeight).toBeGreaterThanOrEqual(observed.bottom)
    expect(a.element.style.width).toBe('100%')
    expect(a.element.style.getPropertyPriority('width')).toBe('important')
    expect(a.element.style.height).toBe('100%')
    expect(a.element.style.getPropertyPriority('height')).toBe('')
    expect(a.element.style.opacity).toBe('0.75')
    expect(a.element.style.outlineColor).toBe('red')
    expect(originalStyle).toContain('width: 100% !important')
    expect(a.element.parentElement).toBe(a.slot)
    expect(a.slot.getAttribute('style')).toBe(originalSlotStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(h.visibleSize).toEqual({ width: originalBounds.width, height: originalBounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshots(h.events)).toEqual([{ type: 'screenshot', tabId: 'a', kind: 'full', dataUrl: 'data:image/png;base64,FULL' }])
    expect(screenshotCommands(a.guest)).toEqual([{ method: 'Page.captureScreenshot', params: {
      format: 'png', fromSurface: true, captureBeyondViewport: true,
      clip: { x: 0, y: 0, width: 585, height: 1334, scale: 1 },
    } }])
  })

  it('coalesces full capture per page and serializes full capture across tabs', async () => {
    const h = captureHarness()
    const a = await h.open('a')
    const b = await h.open('b')
    const first = h.service.capture('a', 'full')
    const same = h.service.capture('a', 'full')
    const next = h.service.capture('b', 'full')
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    expect(screenshotCommands(b.guest)).toHaveLength(0)
    a.guest.captureResult.resolve({ data: 'A' })
    await Promise.all([first, same])
    await assertEventually(() => expect(screenshotCommands(b.guest)).toHaveLength(1))
    b.guest.captureResult.resolve({ data: 'B' })
    await next
    expect(screenshots(h.events).map(event => event.tabId)).toEqual(['a', 'a', 'b'])
  })

  it.each(['viewport', 'selection'] as const)('waits for full capture before a %s screenshot on another tab', async kind => {
    const h = captureHarness()
    const a = await h.open('a')
    const b = await h.open('b')
    const full = h.service.capture('a', 'full')
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    const viewport = kind === 'viewport' ? h.service.capture('b', 'viewport') : (async () => {
      await h.service.message('b', { v: 1, type: 'enter-picker' })
      h.service.handleMessageFromView(b.guest, JSON.stringify({ v: 1, type: 'selection', payload: { element: { tag: 'h1' }, screenshot: { kind: 'region', captureId: 9 } } }))
    })()
    await flush()
    expect(b.guest.capturePage).not.toHaveBeenCalled()
    a.guest.captureResult.resolve({ data: 'A' })
    await full
    await viewport
    await assertEventually(() => expect(b.guest.capturePage).toHaveBeenCalledTimes(1))
    if (kind === 'selection') {
      await assertEventually(() => expect(h.events.filter(event => event.type === 'agent')).toMatchObject([{
        type: 'agent', tabId: 'b', message: { type: 'selection', payload: {
          screenshot: { kind: 'region', captureId: 9, dataUrl: 'data:image/png;base64,VIEWPORT' },
        } },
      }]))
    }
  })

  it('waits for active viewport captures before full capture, while viewport captures can overlap', async () => {
    const h = captureHarness()
    const a = await h.open('a')
    const b = await h.open('b')
    const image = deferred<CaptureImage>()
    a.guest.capturePage.mockReturnValueOnce(image.promise)
    const first = h.service.capture('a', 'viewport')
    await assertEventually(() => expect(a.guest.capturePage).toHaveBeenCalledTimes(1))
    await h.service.capture('b', 'viewport')
    const full = h.service.capture('b', 'full')
    await flush()
    expect(screenshotCommands(b.guest)).toHaveLength(0)
    image.resolve({ toDataURL: () => 'data:image/png;base64,A' })
    await first
    await assertEventually(() => expect(screenshotCommands(b.guest)).toHaveLength(1))
    b.guest.captureResult.resolve({ data: 'B' })
    await full
  })

  it.each(['host-attached', 'guest-attached', 'host-devtools', 'guest-devtools'] as const)('rejects %s without taking ownership or mutating layout', async busy => {
    const h = captureHarness()
    const a = await h.open('a')
    const style = a.element.getAttribute('style')
    const debuggerApi = busy.startsWith('host') ? h.hostDebugger : a.guest.debugger
    if (busy.endsWith('attached')) debuggerApi.attached = true
    else debuggerApi.devToolsOpened = true
    const pending = h.service.capture('a', 'full')
    // Old implementations start a screenshot here; release it so RED cannot stall.
    a.guest.captureResult.resolve({ data: 'SHOULD-NOT-CAPTURE' })
    await expect(pending).rejects.toThrow()
    expect(a.element.getAttribute('style')).toBe(style)
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(h.hostDebugger.commands).toHaveLength(0)
    expect(debuggerApi.detach).not.toHaveBeenCalled()
    expect(screenshots(h.events)).toHaveLength(0)
  })

  it('restores element and host surface when the guest screenshot rejects', async () => {
    const h = captureHarness()
    const a = await h.open('a')
    const style = a.element.getAttribute('style')
    const pending = h.service.capture('a', 'full').then(
      () => ({ status: 'fulfilled' as const }),
      (error: unknown) => ({ status: 'rejected' as const, error }),
    )
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    a.guest.captureResult.reject(new Error('fixture capture failed'))
    const outcome = await pending
    expect(outcome.status).toBe('rejected')
    if (outcome.status !== 'rejected') throw new Error('fixture capture unexpectedly succeeded')
    expect(outcome.error).toBeInstanceOf(Error)
    expect((outcome.error as Error).message).toBe('fixture capture failed')
    expect(a.element.getAttribute('style')).toBe(style)
    expect(h.visibleSize).toEqual({ width: 600, height: 728 })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshots(h.events)).toHaveLength(0)
  })

  it('holds the writer until the actual scroll restoration promise settles, past the capture timeout', async () => {
    const captureTimeoutMs = 200
    const h = captureHarness({ captureTimeoutMs })
    const a = await h.open('a')
    const b = await h.open('b')
    const restore = deferred<void>()
    a.guest.scrollRestoreGate = restore
    cleanups.push(() => restore.resolve())
    let fullSettled = false
    let viewportSettled = false
    const full = h.service.capture('a', 'full').then(
      () => {
        fullSettled = true
        return { status: 'fulfilled' as const }
      },
      error => {
        fullSettled = true
        return { status: 'rejected' as const, error }
      },
    )
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    a.guest.captureResult.resolve({ data: 'FULL' })
    await assertEventually(() => expect(a.guest.scrollCalls).toBe(1))
    const viewport = h.service.capture('b', 'viewport').then(
      () => {
        viewportSettled = true
        return { status: 'fulfilled' as const }
      },
      error => {
        viewportSettled = true
        return { status: 'rejected' as const, error }
      },
    )
    await new Promise(resolve => setTimeout(resolve, captureTimeoutMs + 100))
    expect(fullSettled).toBe(false)
    // The existing viewport UI deadline includes time spent queued. Expiry
    // must not make the still-running writer release or start native capture.
    expect(viewportSettled).toBe(true)
    const expiredViewport = await viewport
    expect(expiredViewport.status).toBe('rejected')
    expect(String('error' in expiredViewport ? expiredViewport.error : '')).toContain('timed out')
    expect(b.guest.capturePage).not.toHaveBeenCalled()
    expect(h.hostDebugger.attached).toBe(true)
    restore.resolve()
    expect(await full).toEqual({ status: 'fulfilled' })
    expect(b.guest.capturePage).not.toHaveBeenCalled()
    await h.service.capture('b', 'viewport')
    expect(b.guest.capturePage).toHaveBeenCalledTimes(1)
    expect(h.hostDebugger.attached).toBe(false)
    expect(screenshots(h.events).map(event => event.tabId).sort()).toEqual(['a', 'b'])
  })

  it('does not restore an old scroll in a new document when evaluation resumes after navigation', async () => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const b = await h.open('b')
    const originalStyle = a.element.getAttribute('style')
    const evaluation = { entered: deferred<void>(), release: deferred<void>() }
    cleanups.push(() => evaluation.release.resolve())
    const hostReply = h.hostDebugger.reply
    h.hostDebugger.reply = async (method, params) => {
      const result = await hostReply(method, params)
      // Guest scroll restoration follows host clear. Delay real evaluation at
      // that boundary, rather than recognizing or replacing script text.
      if (method === 'Emulation.clearDeviceMetricsOverride') a.guest.nextEvaluationGate = evaluation
      return result
    }
    const full = h.service.capture('a', 'full').then(
      () => ({ status: 'fulfilled' as const }),
      error => ({ status: 'rejected' as const, error }),
    )
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    a.guest.captureResult.resolve({ data: 'OLD-DOCUMENT' })
    await evaluation.entered.promise
    const oldDocument = a.guest.realm.document
    const replacementScroll = { x: 113, y: 227 }
    a.guest.replaceDocument(replacementScroll)
    const replacement = a.guest.realm
    const replacementHtml = replacement.document.body.innerHTML
    const documentToken = Symbol.for('cc-haha.workspace-browser.capture-document')
    expect(replacement.document).not.toBe(oldDocument)
    expect(replacement.document.URL).toBe(oldDocument.URL)
    expect(Reflect.has(replacement.window, documentToken)).toBe(false)
    a.guest.emit('did-start-navigation', {}, a.guest.getURL(), false, true)
    evaluation.release.resolve()
    expect((await full).status).toBe('rejected')
    expect(a.guest.scroll).toEqual(replacementScroll)
    expect(a.guest.scrollCalls).toBe(0)
    expect(replacement.document.body.innerHTML).toBe(replacementHtml)
    // Old release must not initialize a token Map in the replacement realm.
    expect(Reflect.has(replacement.window, documentToken)).toBe(false)
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(h.visibleSize).toEqual({ width: h.bounds.width, height: h.bounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshots(h.events)).toHaveLength(0)
    await h.service.capture('b', 'viewport')
    expect(b.guest.capturePage).toHaveBeenCalledTimes(1)
  })

  it('removes the vertical gutter before narrowing so text does not wrap into a longer document', async () => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    a.guest.layoutHysteresis = true
    const originalLayout = { ...a.guest.layoutState() }
    const originalStyle = a.element.getAttribute('style')
    const originalSlotStyle = a.slot.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const box = (element: Element) => {
      const value = element.getBoundingClientRect()
      return { x: value.x, y: value.y, width: value.width, height: value.height }
    }
    const originalBody = box(a.guest.realm.document.body)
    const originalMain = box(a.guest.realm.document.querySelector('main')!)
    expect(originalLayout).toMatchObject({ width: 600, height: 600, clientWidth: 585, gutter: 15, bodyWidth: 537, bodyHeight: 1285.4375 })
    a.guest.captureResult.resolve({ data: 'FULL' })
    const outcome = await h.service.capture('a', 'full').then(
      () => ({ ok: true as const }),
      error => ({ ok: false as const, error: String(error) }),
    )
    expect(outcome).toEqual({ ok: true })
    expect(a.guest.layoutHistory).toEqual(expect.arrayContaining([
      expect.objectContaining({ width: 600, height: 1334, clientWidth: 600, gutter: 0, bodyWidth: 552, bodyHeight: 1285.4375 }),
      expect.objectContaining({ width: 585, height: 1334, clientWidth: 585, gutter: 0, bodyWidth: 537, bodyHeight: 1285.4375 }),
    ]))
    expect(a.guest.layoutHistory.some(state => state.width === 585 && state.height === 1334 && state.clientWidth === 570)).toBe(false)
    expect(a.guest.layoutState()).toEqual(originalLayout)
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(a.slot.getAttribute('style')).toBe(originalSlotStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(box(a.guest.realm.document.body)).toEqual(originalBody)
    expect(box(a.guest.realm.document.querySelector('main')!)).toEqual(originalMain)
    expect(h.visibleSize).toEqual({ width: h.bounds.width, height: h.bounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshots(h.events)).toEqual([{ type: 'screenshot', tabId: 'a', kind: 'full', dataUrl: 'data:image/png;base64,FULL' }])
  })

  it.each(['first', 'second'] as const)('preserves another owner sizing edit before the %s resize and refuses the image', async stage => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const reply = a.guest.debugger.reply
    let expandedSamples = 0
    let edited = false
    a.guest.debugger.reply = async (method, params) => {
      const result = await reply(method, params)
      if (method === 'Page.getLayoutMetrics' && !edited) {
        const state = a.guest.layoutState()
        if (state.width === 600 && state.height === 1334 && state.clientWidth === 600) expandedSamples += 1
        // First: mutate during original sampling, after the element record was
        // saved. Second: mutate after the third identical height-stage sample
        // was collected, before the next renderer execution can narrow it.
        if (stage === 'first' || expandedSamples === 3) {
          const external = h.host.document.createElement('div')
          for (const name of Array.from(a.element.style)) {
            if (name !== 'width') external.style.setProperty(name, a.element.style.getPropertyValue(name), a.element.style.getPropertyPriority(name))
          }
          external.style.setProperty('width', '611px')
          a.element.setAttribute('style', external.getAttribute('style')!)
          edited = true
        }
      }
      return result
    }
    // A broken transaction must be able to finish, so its attempted screenshot
    // is answered rather than left pending. Assertions require zero such calls.
    a.guest.captureResult.resolve({ data: 'SHOULD-NOT-CAPTURE' })
    let failure: unknown
    await h.service.capture('a', 'full').catch(error => { failure = error })
    expect(edited).toBe(true)
    expect(failure).toBeDefined()
    expect(a.element.style.width).toBe('611px')
    expect(a.element.style.getPropertyPriority('width')).toBe('')
    expect(a.element.style.height).toBe('100%')
    expect(a.element.style.getPropertyPriority('height')).toBe('')
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(screenshots(h.events)).toHaveLength(0)
    expect(h.visibleSize).toEqual({ width: h.bounds.width, height: h.bounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
  })

  it('restores CSSOM-canonicalized fractional sizes after a later preparation failure', async () => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const hostZoom = 1.1
    const guestZoom = 1.25
    const guestDpr = 2 * guestZoom
    const originalStyle = a.element.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const originalBounds = { ...h.bounds }
    const hostBox = () => ({
      width: a.element.style.width.endsWith('px') ? Number.parseFloat(a.element.style.width) : 600,
      height: a.element.style.height.endsWith('px') ? Number.parseFloat(a.element.style.height) : 600,
    })
    h.parent.webContents.getZoomFactor = () => hostZoom
    Object.defineProperties(h.host.window, {
      innerWidth: { configurable: true, get: () => Math.round(h.visibleSize.width / hostZoom) },
      innerHeight: { configurable: true, get: () => Math.round(h.visibleSize.height / hostZoom) },
      devicePixelRatio: { configurable: true, get: () => 2 * hostZoom },
    })
    for (const node of [h.host.document.documentElement, h.host.document.body]) {
      Object.defineProperties(node, {
        clientWidth: { configurable: true, get: () => h.host.window.innerWidth },
        clientHeight: { configurable: true, get: () => h.host.window.innerHeight },
        scrollWidth: { configurable: true, get: () => h.host.window.innerWidth },
        scrollHeight: { configurable: true, get: () => h.host.window.innerHeight },
      })
    }
    a.guest.zoom = guestZoom
    a.guest.measure = () => {
      const box = hostBox()
      return { width: box.width * hostZoom / guestZoom, height: box.height * hostZoom / guestZoom }
    }
    a.element.getBoundingClientRect = () => {
      const { width, height } = hostBox()
      return { x: 10, y: 90, left: 10, top: 90, right: 10 + width, bottom: 90 + height, width, height, toJSON: () => ({}) }
    }
    Object.defineProperties(a.guest.realm.window, {
      innerWidth: { configurable: true, get: () => Math.round(a.guest.measure().width) },
      innerHeight: { configurable: true, get: () => Math.round(a.guest.measure().height) },
      devicePixelRatio: { configurable: true, get: () => guestDpr },
    })
    Object.defineProperties(a.guest.realm.document.documentElement, {
      clientWidth: { configurable: true, get: () => Math.round(a.guest.layoutState().clientWidth) },
      clientHeight: { configurable: true, get: () => Math.round(a.guest.layoutState().clientHeight) },
      scrollWidth: { configurable: true, get: () => Math.round(a.guest.layoutState().scrollWidth) },
      scrollHeight: { configurable: true, get: () => Math.round(a.guest.layoutState().scrollHeight) },
    })
    // Model native CSSOM serialization through the actual realm's setter and
    // getter. The shim changes only its numeric px precision, never its reads.
    const writes: Array<{ property: string, requested: string, serialized: string }> = []
    const setProperty = a.element.style.setProperty.bind(a.element.style)
    Object.defineProperty(a.element.style, 'setProperty', {
      configurable: true,
      value: (property: string, value: string | null, priority?: string) => {
        if ((property === 'width' || property === 'height') && typeof value === 'string' && value.endsWith('px')) {
          setProperty(property, `${Number(Number.parseFloat(value).toFixed(2))}px`, priority)
          writes.push({ property, requested: value, serialized: a.element.style.getPropertyValue(property) })
        } else setProperty(property, value, priority)
      },
    })
    const reply = a.guest.debugger.reply
    const fixtureError = new Error('fixture metrics failed after canonical resize')
    let reachedSecondResize = false
    a.guest.debugger.reply = async (method, params) => {
      if (method !== 'Page.getLayoutMetrics') return await reply(method, params)
      const state = a.guest.layoutState()
      if (!reachedSecondResize && hostBox().width !== 600) {
        reachedSecondResize = true
        throw fixtureError
      }
      const cssViewport = { pageX: a.guest.scroll.x, pageY: a.guest.scroll.y,
        clientWidth: Math.floor(state.clientWidth), clientHeight: Math.floor(state.clientHeight) }
      const physicalViewport = { pageX: a.guest.scroll.x * guestDpr, pageY: a.guest.scroll.y * guestDpr,
        clientWidth: Math.ceil(state.clientWidth * guestDpr), clientHeight: Math.ceil(state.clientHeight * guestDpr) }
      return {
        contentSize: { x: 0, y: 0, width: Math.ceil(state.scrollWidth * guestDpr),
          height: Math.ceil(Math.max(state.bodyHeight + 48, state.height) * guestDpr) },
        cssContentSize: { x: 0, y: 0, width: Math.floor(state.scrollWidth), height: Math.floor(state.scrollHeight) },
        layoutViewport: physicalViewport,
        visualViewport: { ...physicalViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: guestZoom },
        cssLayoutViewport: cssViewport,
        cssVisualViewport: { ...cssViewport, clientWidth: state.clientWidth, clientHeight: state.clientHeight,
          offsetX: 0, offsetY: 0, scale: 1, zoom: guestZoom },
      }
    }
    const outcome = await h.service.capture('a', 'full').then(
      () => ({ status: 'fulfilled' as const }),
      (error: unknown) => ({ status: 'rejected' as const, error }),
    )
    expect(reachedSecondResize).toBe(true)
    expect(writes.filter(write => write.property === 'height')).toHaveLength(2)
    expect(writes.some(write => write.property === 'height' && write.requested !== write.serialized)).toBe(true)
    expect(outcome.status).toBe('rejected')
    if (outcome.status !== 'rejected') throw new Error('fixture capture unexpectedly succeeded')
    expect(outcome.error).toBe(fixtureError)
    expect(outcome.error).toBeInstanceOf(Error)
    expect((outcome.error as Error).message).toBe('fixture metrics failed after canonical resize')
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(h.bounds).toEqual(originalBounds)
    expect(h.visibleSize).toEqual({ width: originalBounds.width, height: originalBounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(screenshots(h.events)).toHaveLength(0)
  })

  it.each(['capture', 'physical edge limit', 'physical pixel limit', 'DIP-rounded edge limit', 'DIP-rounded pixel limit'] as const)('handles %s with a measured fractional column and independent zoom', async variant => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const hostZoom = 1.1
    const guestZoom = 1.25
    const baseDsf = variant === 'DIP-rounded edge limit' ? 3 : 2
    const guestDpr = baseDsf * guestZoom
    const bodyHeight = variant === 'physical edge limit' ? 7000 : variant === 'physical pixel limit' ? 1000
      : variant === 'DIP-rounded edge limit' ? 4320.7 : variant === 'DIP-rounded pixel limit' ? 2510 : 1321.0374755859375
    const initialHostWidth = variant === 'physical pixel limit' ? 6400 : variant === 'DIP-rounded pixel limit' ? 2287.27 : 560
    const hostBox = () => ({
      width: a.element.style.width.endsWith('px') ? Number.parseFloat(a.element.style.width) : initialHostWidth,
      height: a.element.style.height.endsWith('px') ? Number.parseFloat(a.element.style.height) : 600,
    })
    const frame = () => {
      const box = hostBox()
      const physicalWidth = Math.round(box.width * hostZoom * baseDsf)
      const physicalHeight = Math.ceil(box.height * hostZoom * baseDsf)
      const gutter = physicalHeight / guestDpr < bodyHeight + 48 ? 15 * baseDsf : 0
      const columnPhysicalWidth = physicalWidth - gutter
      return { physicalWidth, physicalHeight, columnPhysicalWidth,
        columnWidth: Math.fround(columnPhysicalWidth * Math.fround(1 / guestDpr)) }
    }
    h.parent.webContents.getZoomFactor = () => hostZoom
    Object.defineProperties(h.host.window, {
      innerWidth: { configurable: true, get: () => Math.round(h.visibleSize.width / hostZoom) },
      innerHeight: { configurable: true, get: () => Math.round(h.visibleSize.height / hostZoom) },
      devicePixelRatio: { configurable: true, get: () => baseDsf * hostZoom },
    })
    for (const node of [h.host.document.documentElement, h.host.document.body]) {
      Object.defineProperties(node, {
        clientWidth: { configurable: true, get: () => h.host.window.innerWidth },
        clientHeight: { configurable: true, get: () => h.host.window.innerHeight },
        scrollWidth: { configurable: true, get: () => h.host.window.innerWidth },
        scrollHeight: { configurable: true, get: () => h.host.window.innerHeight },
      })
    }
    a.guest.zoom = guestZoom
    a.guest.measure = () => ({ width: frame().physicalWidth / guestDpr, height: frame().physicalHeight / guestDpr })
    a.guest.layoutState = () => {
      const value = frame()
      const width = value.physicalWidth / guestDpr
      const height = value.physicalHeight / guestDpr
      const state = { width, height, clientWidth: value.columnWidth, clientHeight: height,
        scrollWidth: value.columnWidth, scrollHeight: Math.max(Math.floor(bodyHeight + 48), Math.round(height)),
        physicalHeight: Math.max(Math.ceil((bodyHeight + 48) * guestDpr), value.physicalHeight),
        bodyWidth: value.columnWidth - 48, bodyHeight, gutter: width - value.columnWidth }
      if (JSON.stringify(state) !== JSON.stringify(a.guest.layoutHistory.at(-1))) a.guest.layoutHistory.push(state)
      return state
    }
    a.element.getBoundingClientRect = () => {
      const { width, height } = hostBox()
      const x = 20
      const y = 71.99573516845703
      return { x, y, left: x, top: y, right: x + width, bottom: y + height, width, height, toJSON: () => ({}) }
    }
    Object.defineProperties(a.guest.realm.window, {
      innerWidth: { configurable: true, get: () => Math.round(a.guest.measure().width) },
      innerHeight: { configurable: true, get: () => Math.round(a.guest.measure().height) },
      devicePixelRatio: { configurable: true, get: () => guestDpr },
    })
    Object.defineProperties(a.guest.realm.document.documentElement, {
      clientWidth: { configurable: true, get: () => Math.round(a.guest.layoutState().clientWidth) },
      clientHeight: { configurable: true, get: () => Math.round(a.guest.layoutState().clientHeight) },
      scrollWidth: { configurable: true, get: () => Math.round(a.guest.layoutState().scrollWidth) },
      scrollHeight: { configurable: true, get: () => Math.round(a.guest.layoutState().scrollHeight) },
    })
    Object.defineProperty(a.guest.realm.document.body, 'scrollWidth', {
      configurable: true, get: () => Math.round(a.guest.layoutState().bodyWidth),
    })
    // Replay the observed serialization class while all geometry still derives
    // from actual current CSS, native pixel allocation and guest layout scale.
    const sizeWrites: string[] = []
    const setProperty = a.element.style.setProperty.bind(a.element.style)
    Object.defineProperty(a.element.style, 'setProperty', {
      configurable: true,
      value: (property: string, value: string | null, priority?: string) => {
        if ((property === 'width' || property === 'height') && typeof value === 'string' && value.endsWith('px')) {
          sizeWrites.push(property)
          setProperty(property, `${Number(Number.parseFloat(value).toFixed(2))}px`, priority)
        } else setProperty(property, value, priority)
      },
    })
    const reply = a.guest.debugger.reply
    a.guest.debugger.reply = async (method, params) => {
      if (method !== 'Page.getLayoutMetrics') return await reply(method, params)
      const state = a.guest.layoutState()
      const value = frame()
      const cssViewport = { pageX: a.guest.scroll.x, pageY: a.guest.scroll.y,
        clientWidth: Math.floor(state.clientWidth), clientHeight: Math.floor(state.clientHeight) }
      const physicalViewport = { pageX: a.guest.scroll.x * guestDpr, pageY: a.guest.scroll.y * guestDpr,
        clientWidth: value.columnPhysicalWidth, clientHeight: value.physicalHeight }
      return {
        contentSize: { x: 0, y: 0, width: value.columnPhysicalWidth, height: state.physicalHeight },
        cssContentSize: { x: 0, y: 0, width: Math.floor(state.scrollWidth), height: state.scrollHeight },
        layoutViewport: physicalViewport,
        visualViewport: { ...physicalViewport, offsetX: 0, offsetY: 0, scale: 1, zoom: guestZoom },
        cssLayoutViewport: cssViewport,
        cssVisualViewport: { ...cssViewport, clientWidth: state.clientWidth, clientHeight: state.clientHeight,
          offsetX: 0, offsetY: 0, scale: 1, zoom: guestZoom },
      }
    }
    const documentRect = (element: Element) => {
      const rect = element.getBoundingClientRect()
      return { x: rect.x + a.guest.scroll.x, y: rect.y + a.guest.scroll.y, width: rect.width, height: rect.height }
    }
    const originalStyle = a.element.getAttribute('style')
    const originalSlotStyle = a.slot.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const originalBounds = { ...h.bounds }
    const originalBody = documentRect(a.guest.realm.document.body)
    const originalMain = documentRect(a.guest.realm.document.querySelector('main')!)
    const originalLayout = { ...a.guest.layoutState() }
    const expectedHeight = variant === 'physical edge limit' ? 7048 : variant === 'physical pixel limit' ? 1048
      : variant === 'DIP-rounded edge limit' ? 4369 : variant === 'DIP-rounded pixel limit' ? 2558 : 1370
    const outputWidth = Math.ceil(originalLayout.clientWidth * guestDpr)
    const outputHeight = Math.ceil(expectedHeight * guestDpr)
    const finalOutputWidth = Math.ceil(Math.ceil(originalLayout.clientWidth * guestZoom) * baseDsf)
    const finalOutputHeight = Math.ceil(Math.ceil(expectedHeight * guestZoom) * baseDsf)
    // CSS and host allocations fit in the negative cases; their physical
    // output alone exceeds the existing edge or area budget.
    expect(Math.ceil(originalLayout.clientWidth)).toBeLessThanOrEqual(16_384)
    expect(expectedHeight).toBeLessThanOrEqual(16_384)
    expect(Math.ceil(originalLayout.clientWidth) * expectedHeight).toBeLessThanOrEqual(32_000_000)
    const hostVisibleHeight = Math.ceil(71.99573516845703 * hostZoom + expectedHeight * guestZoom)
    expect(hostVisibleHeight).toBeLessThanOrEqual(16_384)
    expect(h.bounds.width * hostVisibleHeight).toBeLessThanOrEqual(32_000_000)
    if (variant === 'physical edge limit') {
      expect(outputHeight).toBeGreaterThan(16_384)
      expect(outputWidth * outputHeight).toBeLessThanOrEqual(32_000_000)
    } else if (variant === 'physical pixel limit') {
      expect(outputWidth).toBeLessThanOrEqual(16_384)
      expect(outputHeight).toBeLessThanOrEqual(16_384)
      expect(outputWidth * outputHeight).toBeGreaterThan(32_000_000)
    } else if (variant === 'DIP-rounded edge limit' || variant === 'DIP-rounded pixel limit') {
      // The old CSS*DPR estimate fits. Outward DIP rounding is solely what
      // crosses the final allocation boundary, before any owned resize.
      expect(outputWidth).toBeLessThanOrEqual(16_384)
      expect(outputHeight).toBeLessThanOrEqual(16_384)
      expect(outputWidth * outputHeight).toBeLessThanOrEqual(32_000_000)
      if (variant === 'DIP-rounded edge limit') {
        expect(outputHeight).toBe(16_384)
        expect(finalOutputHeight).toBe(16_386)
        expect(finalOutputWidth * finalOutputHeight).toBeLessThanOrEqual(32_000_000)
      } else {
        expect(outputWidth * outputHeight).toBe(31_994_185)
        expect(finalOutputWidth).toBeLessThanOrEqual(16_384)
        expect(finalOutputHeight).toBeLessThanOrEqual(16_384)
        expect(finalOutputWidth * finalOutputHeight).toBe(32_005_584)
      }
    } else {
      expect(frame().columnPhysicalWidth).toBe(1202)
      expect(originalLayout.clientWidth).toBe(480.8000183105469)
      expect(Math.floor(originalLayout.clientWidth)).toBe(480)
      expect(a.guest.realm.document.documentElement.clientWidth).toBe(481)
      expect(originalBody.width).toBe(432.8000183105469)
    }
    let capturedBody: unknown
    let capturedMain: unknown
    a.guest.beforeScreenshot = () => {
      capturedBody = documentRect(a.guest.realm.document.body)
      capturedMain = documentRect(a.guest.realm.document.querySelector('main')!)
      if (variant === 'capture') expect(frame().columnPhysicalWidth).toBe(1202)
    }
    a.guest.captureResult.resolve({ data: 'FRACTIONAL-FULL' })
    const outcome = await h.service.capture('a', 'full').then(
      () => ({ status: 'fulfilled' as const }),
      (error: unknown) => ({ status: 'rejected' as const, error }),
    )
    if (variant === 'capture') {
      expect(outcome).toEqual({ status: 'fulfilled' })
      expect(capturedBody).toEqual(originalBody)
      expect(capturedMain).toEqual(originalMain)
      expect(screenshotCommands(a.guest)).toEqual([{ method: 'Page.captureScreenshot', params: {
        format: 'png', fromSurface: true, captureBeyondViewport: true,
        clip: { x: 0, y: 0, width: 602, height: 1713, scale: 1 },
      } }])
      expect(screenshots(h.events)).toEqual([{ type: 'screenshot', tabId: 'a', kind: 'full', dataUrl: 'data:image/png;base64,FRACTIONAL-FULL' }])
    } else {
      expect(outcome.status).toBe('rejected')
      if (outcome.status !== 'rejected') throw new Error('over-budget capture unexpectedly succeeded')
      expect(outcome.error).toBeInstanceOf(Error)
      expect(String(outcome.error)).toContain('full capture exceeds safety limit')
      expect(screenshotCommands(a.guest)).toHaveLength(0)
      expect(screenshots(h.events)).toHaveLength(0)
      expect(h.hostDebugger.commands).toHaveLength(0)
      expect(sizeWrites).toHaveLength(0)
    }
    expect(documentRect(a.guest.realm.document.body)).toEqual(originalBody)
    expect(documentRect(a.guest.realm.document.querySelector('main')!)).toEqual(originalMain)
    expect(a.guest.layoutState()).toEqual(originalLayout)
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(a.slot.getAttribute('style')).toBe(originalSlotStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(h.bounds).toEqual(originalBounds)
    expect(h.visibleSize).toEqual({ width: originalBounds.width, height: originalBounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
  })

  it.each(['edge', 'area'] as const)('rejects a host physical %s allocation before resizing when the guest output fits', async limit => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const geometry = limit === 'edge'
      ? { hostWidth: 600, hostHeight: 1000, columnWidth: 500, documentHeight: 8100, y: 200 }
      : { hostWidth: 1536, hostHeight: 728, columnWidth: 1280, documentHeight: 6000, y: 72 }
    h.resize(geometry.hostWidth, geometry.hostHeight)
    // Construct the initial native viewport through the existing fake's own
    // reset path. This is fixture setup, not a service debugger command.
    await h.hostDebugger.reply('Emulation.clearDeviceMetricsOverride')
    a.slot.style.left = '20px'
    a.slot.style.top = `${geometry.y}px`
    a.slot.style.width = `${geometry.columnWidth + 15}px`
    a.guest.measure = () => ({
      width: a.element.style.width.endsWith('px') ? Number.parseFloat(a.element.style.width) : geometry.columnWidth + 15,
      height: a.element.style.height.endsWith('px') ? Number.parseFloat(a.element.style.height) : 600,
    })
    a.guest.layoutState = () => {
      const { width, height } = a.guest.measure()
      const gutter = height < geometry.documentHeight ? 15 : 0
      const clientWidth = width - gutter
      const state = { width, height, clientWidth, clientHeight: height,
        scrollWidth: clientWidth, scrollHeight: Math.max(geometry.documentHeight, height),
        physicalHeight: Math.max(geometry.documentHeight, height) * 2,
        bodyWidth: clientWidth - 48, bodyHeight: geometry.documentHeight - 48, gutter }
      if (JSON.stringify(state) !== JSON.stringify(a.guest.layoutHistory.at(-1))) a.guest.layoutHistory.push(state)
      return state
    }
    a.element.getBoundingClientRect = () => {
      const { width, height } = a.guest.measure()
      return { x: 20, y: geometry.y, left: 20, top: geometry.y, right: 20 + width,
        bottom: geometry.y + height, width, height, toJSON: () => ({}) }
    }
    const writes: string[] = []
    const setProperty = a.element.style.setProperty.bind(a.element.style)
    Object.defineProperty(a.element.style, 'setProperty', {
      configurable: true,
      value: (property: string, value: string | null, priority?: string) => {
        if ((property === 'width' || property === 'height') && typeof value === 'string' && value.endsWith('px')) writes.push(property)
        setProperty(property, value, priority)
      },
    })
    const originalStyle = a.element.getAttribute('style')
    const originalSlotStyle = a.slot.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const originalBounds = { ...h.bounds }
    const originalLayout = { ...a.guest.layoutState() }
    const bodyRect = () => {
      const rect = a.guest.realm.document.body.getBoundingClientRect()
      return { x: rect.x + a.guest.scroll.x, y: rect.y + a.guest.scroll.y, width: rect.width, height: rect.height }
    }
    const originalBody = bodyRect()
    const guestPixels = { width: geometry.columnWidth * 2, height: geometry.documentHeight * 2 }
    const hostDips = { width: geometry.hostWidth, height: Math.max(geometry.hostHeight, geometry.y + geometry.documentHeight) }
    const hostPixels = { width: hostDips.width * 2, height: hostDips.height * 2 }
    // CSS, full guest output and final integer-DIP screenshot canvas all fit.
    // Only the separate parent compositor's physical allocation is over budget.
    expect(geometry.columnWidth * geometry.documentHeight).toBeLessThanOrEqual(32_000_000)
    expect(guestPixels.width).toBeLessThanOrEqual(16_384)
    expect(guestPixels.height).toBeLessThanOrEqual(16_384)
    expect(guestPixels.width * guestPixels.height).toBeLessThanOrEqual(32_000_000)
    expect(hostDips.width).toBeLessThanOrEqual(16_384)
    expect(hostDips.height).toBeLessThanOrEqual(16_384)
    expect(hostDips.width * hostDips.height).toBeLessThanOrEqual(32_000_000)
    if (limit === 'edge') {
      expect(hostPixels.height).toBe(16_600)
      expect(hostPixels.width * hostPixels.height).toBeLessThanOrEqual(32_000_000)
    } else {
      expect(guestPixels.width * guestPixels.height).toBe(30_720_000)
      expect(hostPixels.width).toBeLessThanOrEqual(16_384)
      expect(hostPixels.height).toBeLessThanOrEqual(16_384)
      expect(hostPixels.width * hostPixels.height).toBe(37_306_368)
    }
    // An old service may really reach its fake screenshot; let it finish so
    // the refusal boundary fails without an artificial timeout or fixture error.
    a.guest.captureResult.resolve({ data: 'OVER-BUDGET-HOST' })
    const outcome = await h.service.capture('a', 'full').then(
      () => ({ status: 'fulfilled' as const }),
      (error: unknown) => ({ status: 'rejected' as const, error }),
    )
    expect(writes).toHaveLength(0)
    expect(h.hostDebugger.commands).toHaveLength(0)
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(screenshots(h.events)).toHaveLength(0)
    expect(outcome.status).toBe('rejected')
    if (outcome.status !== 'rejected') throw new Error('over-budget host capture unexpectedly succeeded')
    expect(outcome.error).toBeInstanceOf(Error)
    expect(String(outcome.error)).toContain('full capture exceeds safety limit')
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(a.slot.getAttribute('style')).toBe(originalSlotStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(a.guest.layoutState()).toEqual(originalLayout)
    expect(bodyRect()).toEqual(originalBody)
    expect(h.bounds).toEqual(originalBounds)
    expect(h.visibleSize).toEqual({ width: originalBounds.width, height: originalBounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
  })

  it.each(['before expansion', 'after host expansion'] as const)('restores owned state when read-only layout metrics hang %s', async stage => {
    const h = captureHarness({ captureTimeoutMs: 200 })
    const a = await h.open('a')
    const originalStyle = a.element.getAttribute('style')
    const originalSlotStyle = a.slot.getAttribute('style')
    const originalScroll = { ...a.guest.scroll }
    const reply = a.guest.debugger.reply
    const lateMetrics = deferred<unknown>()
    cleanups.push(() => lateMetrics.resolve({}))
    let suspended = false
    let visibleHeightWhileSuspended = 0
    a.guest.debugger.reply = async (method, params) => {
      if (method === 'Page.getLayoutMetrics' && !suspended &&
        (stage === 'before expansion' || h.visibleSize.height > h.bounds.height)) {
        suspended = true
        visibleHeightWhileSuspended = h.visibleSize.height
        return await lateMetrics.promise
      }
      return await reply(method, params)
    }
    let completed = false
    let failure: unknown
    const pending = h.service.capture('a', 'full').then(
      () => { completed = true },
      error => {
        completed = true
        failure = error
      },
    )
    await assertEventually(() => expect(suspended).toBe(true))
    await assertEventually(() => expect(completed).toBe(true))
    await pending
    expect(failure).toBeInstanceOf(Error)
    expect(String(failure)).toContain('browser capture layout metrics timed out')
    if (stage === 'after host expansion') expect(visibleHeightWhileSuspended).toBeGreaterThan(h.bounds.height)
    expect(a.element.getAttribute('style')).toBe(originalStyle)
    expect(a.slot.getAttribute('style')).toBe(originalSlotStyle)
    expect(a.guest.scroll).toEqual(originalScroll)
    expect(h.visibleSize).toEqual({ width: h.bounds.width, height: h.bounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(screenshots(h.events)).toHaveLength(0)
    const restored = { style: a.element.getAttribute('style'), visibleSize: { ...h.visibleSize }, scroll: { ...a.guest.scroll } }
    lateMetrics.resolve(await reply('Page.getLayoutMetrics'))
    await flush()
    expect({ style: a.element.getAttribute('style'), visibleSize: h.visibleSize, scroll: a.guest.scroll }).toEqual(restored)
    expect(screenshots(h.events)).toHaveLength(0)
  })

  it('never screenshots when the registered host element is missing', async () => {
    const h = captureHarness()
    const a = await h.open('a')
    a.element.remove()
    const pending = h.service.capture('a', 'full')
    a.guest.captureResult.resolve({ data: 'SHOULD-NOT-CAPTURE' })
    await expect(pending).rejects.toThrow()
    expect(screenshotCommands(a.guest)).toHaveLength(0)
    expect(h.hostDebugger.attached).toBe(false)
    expect(a.guest.debugger.attached).toBe(false)
    expect(screenshots(h.events)).toHaveLength(0)
  })

  it.each(['navigate', 'resize', 'zoom', 'close-reopen'] as const)('does not publish stale pixels or overwrite a replacement after %s', async change => {
    const h = captureHarness()
    const a = await h.open('a')
    let replacement: HTMLElement | undefined
    const pending = h.service.capture('a', 'full')
    const settled = pending.catch(() => {})
    await assertEventually(() => expect(screenshotCommands(a.guest)).toHaveLength(1))
    if (change === 'navigate') a.guest.emit('did-start-navigation', {}, 'https://fixture.test/new', false, true)
    if (change === 'resize') h.resize(800, 500)
    if (change === 'zoom') h.service.setZoom('a', 1.25)
    if (change === 'close-reopen') {
      h.service.close('a')
      a.element.remove()
      replacement = (await h.open('a')).element
      replacement.style.height = '777px'
    }
    a.guest.captureResult.resolve({ data: 'STALE' })
    await settled
    expect(screenshots(h.events)).toHaveLength(0)
    expect(a.element.style.height).toBe('100%')
    expect(replacement?.style.height ?? '777px').toBe('777px')
    expect(h.visibleSize).toEqual({ width: h.bounds.width, height: h.bounds.height })
    expect(h.hostDebugger.attached).toBe(false)
    if (change === 'zoom') expect(a.guest.zoom).toBe(1.25)
  })
})
