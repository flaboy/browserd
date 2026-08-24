import RFB from '@novnc/novnc/lib/rfb.js'
import keysyms from '@novnc/novnc/lib/input/keysym.js'
import keysymdef from '@novnc/novnc/lib/input/keysymdef.js'
import './style.css'

const statusEl = document.querySelector<HTMLDivElement>('#status')
const screenEl = document.querySelector<HTMLDivElement>('#screen')
const pointerEl = document.querySelector<HTMLDivElement>('#virtual-pointer')
const mobileKeyboardEl = document.querySelector<HTMLDivElement>('#mobile-keyboard')
const mobileKeyboardInput = document.querySelector<HTMLTextAreaElement>('#mobile-keyboard-input')
const mobileKeyboardButton = document.querySelector<HTMLButtonElement>('#mobile-keyboard-button')
const mobileKeyboardToolbar = document.querySelector<HTMLDivElement>('#mobile-keyboard-toolbar')
let lastPointerSnapshot: PointerSnapshot | null = null
let pointerRenderRetry = 0
let mobileKeyboardComposing = false

type RFBClient = {
  clipboardPasteFrom: (text: string) => void
  sendKey: (keysym: number, code?: string, down?: boolean) => void
  focus: (options?: FocusOptions) => void
}

type PointerSnapshot = {
  x: number
  y: number
  viewportWidth: number
  viewportHeight: number
  contentOffsetX: number
  contentOffsetY: number
  visible: boolean
  buttonDown: boolean
}

function setStatus(text: string, error = false) {
  if (!statusEl) return
  statusEl.textContent = text
  statusEl.dataset.error = error ? 'true' : 'false'
  statusEl.dataset.state = 'message'
}

function hideStatus() {
  if (!statusEl) return
  statusEl.textContent = ''
  statusEl.dataset.error = 'false'
  statusEl.dataset.state = 'hidden'
}

function setScreenBusy(busy: boolean) {
  if (busy) {
    screenEl?.setAttribute('aria-busy', 'true')
  } else {
    screenEl?.setAttribute('aria-busy', 'false')
  }
}

function websockifyPath(): string {
  const params = new URLSearchParams(window.location.search)
  const explicit = params.get('path')?.trim()
  if (explicit) return explicit.replace(/^\/+/, '')

  const match = window.location.pathname.match(/^\/v\/([^/]+)/)
  if (!match) {
    throw new Error('live token path is required')
  }
  return `v/${match[1]}/websockify`
}

function liveToken(): string {
  const explicit = websockifyPath()
  const explicitMatch = explicit.match(/^v\/([^/]+)\//)
  if (explicitMatch) return explicitMatch[1]

  const pathMatch = window.location.pathname.match(/^\/v\/([^/]+)/)
  if (pathMatch) return pathMatch[1]

  throw new Error('live token path is required')
}

function websocketURL(path: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/${path}`
}

function pointerEventsURL(): string {
  return `/v/${liveToken()}/pointer-events`
}

function canvasBounds(): DOMRect | null {
  if (!screenEl) return null
  return (screenEl.querySelector('canvas') ?? screenEl).getBoundingClientRect()
}

function mapPointerToScreen(snapshot: PointerSnapshot): { x: number; y: number } | null {
  if (snapshot.viewportWidth <= 0 || snapshot.viewportHeight <= 0) return null
  const bounds = canvasBounds()
  if (!bounds || bounds.width <= 0 || bounds.height <= 0) return null
  const contentOffsetX = snapshot.contentOffsetX ?? 0
  const contentOffsetY = snapshot.contentOffsetY ?? 0
  const desktopWidth = contentOffsetX + snapshot.viewportWidth
  const desktopHeight = contentOffsetY + snapshot.viewportHeight
  if (desktopWidth <= 0 || desktopHeight <= 0) return null
  return {
    x: bounds.left + ((contentOffsetX + snapshot.x) / desktopWidth) * bounds.width,
    y: bounds.top + ((contentOffsetY + snapshot.y) / desktopHeight) * bounds.height,
  }
}

function viewportCenter(): { x: number; y: number } {
  return { x: window.innerWidth / 2, y: window.innerHeight / 2 }
}

function setPointerPosition(point: { x: number; y: number }, buttonDown = false) {
  if (!pointerEl) return
  pointerEl.style.setProperty('--pointer-x', `${point.x}px`)
  pointerEl.style.setProperty('--pointer-y', `${point.y}px`)
  pointerEl.dataset.visible = 'true'
  pointerEl.dataset.buttonDown = buttonDown ? 'true' : 'false'
}

function renderPointer(snapshot: PointerSnapshot): boolean {
  if (!pointerEl) return false
  const point = mapPointerToScreen(snapshot)
  if (!snapshot.visible) {
    setPointerPosition(viewportCenter(), false)
    return true
  }
  if (!point) {
    setPointerPosition(viewportCenter(), snapshot.buttonDown)
    return false
  }
  setPointerPosition(point, snapshot.buttonDown)
  return true
}

function renderLastPointer() {
  if (!lastPointerSnapshot) {
    setPointerPosition(viewportCenter(), false)
    return
  }
  if (!renderPointer(lastPointerSnapshot)) {
    window.cancelAnimationFrame(pointerRenderRetry)
    pointerRenderRetry = window.requestAnimationFrame(renderLastPointer)
  }
}

function connectPointerOverlay() {
  renderLastPointer()
  const events = new EventSource(pointerEventsURL())
  events.addEventListener('pointer', (event: MessageEvent<string>) => {
    lastPointerSnapshot = JSON.parse(event.data) as PointerSnapshot
    renderLastPointer()
  })
  events.addEventListener('error', () => {
    renderLastPointer()
  })
  window.addEventListener('beforeunload', () => events.close(), { once: true })
  window.addEventListener('resize', renderLastPointer)
}

const specialKeys: Record<string, { keysym: number; code: string }> = {
  Backspace: { keysym: keysyms.XK_BackSpace, code: 'Backspace' },
  Enter: { keysym: keysyms.XK_Return, code: 'Enter' },
  Tab: { keysym: keysyms.XK_Tab, code: 'Tab' },
  Escape: { keysym: keysyms.XK_Escape, code: 'Escape' },
}

function mobileVirtualKeyboard(): { overlaysContent?: boolean; show?: () => void; hide?: () => void; boundingRect?: DOMRect } | null {
  return 'virtualKeyboard' in navigator
    ? ((navigator as Navigator & {
        virtualKeyboard?: { overlaysContent?: boolean; show?: () => void; hide?: () => void; boundingRect?: DOMRect }
      }).virtualKeyboard ?? null)
    : null
}

function updateMobileKeyboardInset() {
  const viewport = window.visualViewport
  const viewportInset = viewport ? Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop) : 0
  const keyboardInset = mobileVirtualKeyboard()?.boundingRect?.height ?? 0
  document.documentElement.style.setProperty('--mobile-keyboard-inset', `${Math.max(viewportInset, keyboardInset)}px`)
}

function focusMobileKeyboardInput(showKeyboard = true) {
  if (!mobileKeyboardInput) return
  mobileKeyboardEl?.setAttribute('data-active', 'true')
  mobileKeyboardInput.focus({ preventScroll: true })
  mobileKeyboardInput.setSelectionRange(mobileKeyboardInput.value.length, mobileKeyboardInput.value.length)
  if (showKeyboard) {
    mobileVirtualKeyboard()?.show?.()
  }
  updateMobileKeyboardInset()
}

function blurMobileKeyboardInput() {
  mobileKeyboardInput?.blur()
  mobileKeyboardEl?.setAttribute('data-active', 'false')
  mobileVirtualKeyboard()?.hide?.()
  updateMobileKeyboardInset()
}

function sendSpecialKey(rfb: RFBClient, key: string) {
  const special = specialKeys[key]
  if (!special) return
  rfb.sendKey(special.keysym, special.code)
}

function shouldPasteText(text: string): boolean {
  return Array.from(text).length > 1 || Array.from(text).some((char) => (char.codePointAt(0) ?? 0) > 0x7e)
}

function sendPasteShortcut(rfb: RFBClient) {
  rfb.sendKey(keysyms.XK_Control_L, 'ControlLeft', true)
  rfb.sendKey(keysyms.XK_v, 'KeyV')
  rfb.sendKey(keysyms.XK_Control_L, 'ControlLeft', false)
}

function sendText(rfb: RFBClient, text: string) {
  if (shouldPasteText(text)) {
    rfb.clipboardPasteFrom(text)
    window.setTimeout(() => sendPasteShortcut(rfb), 60)
    return
  }
  for (const char of Array.from(text)) {
    if (char === '\n') {
      sendSpecialKey(rfb, 'Enter')
      continue
    }
    const codePoint = char.codePointAt(0)
    if (codePoint === undefined) continue
    rfb.sendKey(keysymdef.lookup(codePoint), '')
  }
}

function flushMobileKeyboardInput(rfb: RFBClient) {
  if (!mobileKeyboardInput || mobileKeyboardComposing) return
  const text = mobileKeyboardInput.value
  if (!text) return
  mobileKeyboardInput.value = ''
  sendText(rfb, text)
}

function connectMobileKeyboardBridge(rfb: RFBClient) {
  if (!mobileKeyboardInput || !mobileKeyboardButton || !mobileKeyboardToolbar) return

  const virtualKeyboard = mobileVirtualKeyboard()
  if ('virtualKeyboard' in navigator && navigator.virtualKeyboard && 'overlaysContent' in navigator.virtualKeyboard) {
    navigator.virtualKeyboard.overlaysContent = true
  }

  mobileKeyboardButton.addEventListener('pointerdown', (event) => {
    event.preventDefault()
    focusMobileKeyboardInput()
  })
  mobileKeyboardButton.addEventListener('click', (event) => {
    event.preventDefault()
    focusMobileKeyboardInput()
  })

  mobileKeyboardToolbar.addEventListener('pointerdown', (event) => {
    const target = event.target as HTMLElement | null
    const keyButton = target?.closest<HTMLButtonElement>('[data-key]')
    const closeButton = target?.closest<HTMLButtonElement>('[data-mobile-keyboard-close]')
    if (!keyButton && !closeButton) return
    event.preventDefault()
    if (closeButton) {
      blurMobileKeyboardInput()
      return
    }
    sendSpecialKey(rfb, keyButton.dataset.key ?? '')
    focusMobileKeyboardInput()
  })

  mobileKeyboardInput.addEventListener('compositionstart', () => {
    mobileKeyboardComposing = true
  })
  mobileKeyboardInput.addEventListener('compositionend', () => {
    mobileKeyboardComposing = false
    window.setTimeout(() => flushMobileKeyboardInput(rfb), 0)
  })
  mobileKeyboardInput.addEventListener('beforeinput', (event) => {
    const inputEvent = event as InputEvent
    if (inputEvent.isComposing) return
    if (inputEvent.inputType === 'deleteContentBackward') {
      event.preventDefault()
      sendSpecialKey(rfb, 'Backspace')
      return
    }
    if (inputEvent.inputType === 'insertLineBreak') {
      event.preventDefault()
      sendSpecialKey(rfb, 'Enter')
    }
  })
  mobileKeyboardInput.addEventListener('input', (event) => {
    const inputEvent = event as InputEvent
    if (inputEvent.isComposing) return
    flushMobileKeyboardInput(rfb)
  })
  mobileKeyboardInput.addEventListener('keydown', (event) => {
    if (event.key in specialKeys) {
      event.preventDefault()
      sendSpecialKey(rfb, event.key)
    }
  })
  mobileKeyboardInput.addEventListener('blur', () => {
    mobileKeyboardEl?.setAttribute('data-active', 'false')
    updateMobileKeyboardInset()
  })

  window.visualViewport?.addEventListener('resize', updateMobileKeyboardInset)
  window.visualViewport?.addEventListener('scroll', updateMobileKeyboardInset)
  virtualKeyboard?.addEventListener?.('geometrychange', updateMobileKeyboardInset)
  window.addEventListener('resize', updateMobileKeyboardInset)
  updateMobileKeyboardInset()
}

function connect() {
  if (!screenEl) {
    throw new Error('screen container is missing')
  }
  setScreenBusy(true)
  const rfb = new RFB(screenEl, websocketURL(websockifyPath()))
  rfb.viewOnly = false
  rfb.scaleViewport = true
  rfb.resizeSession = false
  rfb.showDotCursor = true

  rfb.addEventListener('connect', () => {
    setScreenBusy(false)
    hideStatus()
    renderLastPointer()
  })
  rfb.addEventListener('disconnect', (event: Event) => {
    const detail = (event as CustomEvent).detail as { clean?: boolean } | undefined
    setScreenBusy(false)
    setStatus(detail?.clean ? 'Disconnected' : 'Connection lost', !detail?.clean)
    if (detail?.clean) {
      window.setTimeout(() => window.close(), 250)
    }
  })
  rfb.addEventListener('securityfailure', () => {
    setScreenBusy(false)
    setStatus('Security failure', true)
  })
  rfb.addEventListener('credentialsrequired', () => {
    setScreenBusy(false)
    setStatus('Credentials required', true)
  })
  connectPointerOverlay()
  connectMobileKeyboardBridge(rfb)
}

try {
  connect()
} catch (error) {
  setScreenBusy(false)
  setStatus(error instanceof Error ? error.message : String(error), true)
}
