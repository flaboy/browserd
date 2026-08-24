import RFB from '@novnc/novnc/lib/rfb.js'
import './style.css'

const statusEl = document.querySelector<HTMLDivElement>('#status')
const screenEl = document.querySelector<HTMLDivElement>('#screen')
const pointerEl = document.querySelector<HTMLDivElement>('#virtual-pointer')
let lastPointerSnapshot: PointerSnapshot | null = null
let pointerRenderRetry = 0

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
}

try {
  connect()
} catch (error) {
  setScreenBusy(false)
  setStatus(error instanceof Error ? error.message : String(error), true)
}
