import RFB from '@novnc/novnc/lib/rfb.js'
import './style.css'

const statusEl = document.querySelector<HTMLDivElement>('#status')
const screenEl = document.querySelector<HTMLDivElement>('#screen')
const pointerEl = document.querySelector<HTMLDivElement>('#virtual-pointer')

type PointerSnapshot = {
  x: number
  y: number
  viewportWidth: number
  viewportHeight: number
  visible: boolean
  buttonDown: boolean
}

function setStatus(text: string, error = false) {
  if (!statusEl) return
  statusEl.textContent = text
  statusEl.dataset.error = error ? 'true' : 'false'
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
  return {
    x: bounds.left + (snapshot.x / snapshot.viewportWidth) * bounds.width,
    y: bounds.top + (snapshot.y / snapshot.viewportHeight) * bounds.height,
  }
}

function renderPointer(snapshot: PointerSnapshot) {
  if (!pointerEl) return
  const point = mapPointerToScreen(snapshot)
  if (!snapshot.visible || !point) {
    pointerEl.dataset.visible = 'false'
    pointerEl.dataset.buttonDown = 'false'
    return
  }
  pointerEl.style.setProperty('--pointer-x', `${point.x}px`)
  pointerEl.style.setProperty('--pointer-y', `${point.y}px`)
  pointerEl.dataset.visible = 'true'
  pointerEl.dataset.buttonDown = snapshot.buttonDown ? 'true' : 'false'
}

function connectPointerOverlay() {
  const events = new EventSource(pointerEventsURL())
  events.addEventListener('pointer', (event: MessageEvent<string>) => {
    renderPointer(JSON.parse(event.data) as PointerSnapshot)
  })
  events.addEventListener('error', () => {
    if (!pointerEl) return
    pointerEl.dataset.visible = 'false'
    pointerEl.dataset.buttonDown = 'false'
  })
  window.addEventListener('beforeunload', () => events.close(), { once: true })
}

function connect() {
  if (!screenEl) {
    throw new Error('screen container is missing')
  }
  const rfb = new RFB(screenEl, websocketURL(websockifyPath()))
  rfb.viewOnly = false
  rfb.scaleViewport = true
  rfb.resizeSession = false
  rfb.showDotCursor = true

  rfb.addEventListener('connect', () => setStatus('Connected'))
  rfb.addEventListener('disconnect', (event: Event) => {
    const detail = (event as CustomEvent).detail as { clean?: boolean } | undefined
    setStatus(detail?.clean ? 'Disconnected' : 'Connection lost', !detail?.clean)
    if (detail?.clean) {
      window.setTimeout(() => window.close(), 250)
    }
  })
  rfb.addEventListener('securityfailure', () => setStatus('Security failure', true))
  rfb.addEventListener('credentialsrequired', () => setStatus('Credentials required', true))
  connectPointerOverlay()
}

try {
  connect()
} catch (error) {
  setStatus(error instanceof Error ? error.message : String(error), true)
}
