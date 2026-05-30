import RFB from '@novnc/novnc/lib/rfb.js'
import './style.css'

const statusEl = document.querySelector<HTMLDivElement>('#status')
const screenEl = document.querySelector<HTMLDivElement>('#screen')

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

function websocketURL(path: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}/${path}`
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
  })
  rfb.addEventListener('securityfailure', () => setStatus('Security failure', true))
  rfb.addEventListener('credentialsrequired', () => setStatus('Credentials required', true))
}

try {
  connect()
} catch (error) {
  setStatus(error instanceof Error ? error.message : String(error), true)
}
