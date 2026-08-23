import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import assert from 'node:assert/strict'

const source = readFileSync(join(import.meta.dirname, 'src/main.ts'), 'utf8')
const html = readFileSync(join(import.meta.dirname, 'index.html'), 'utf8')
const style = readFileSync(join(import.meta.dirname, 'src/style.css'), 'utf8')

assert.match(source, /rfb\.viewOnly\s*=\s*false/, 'browser live viewer must allow mouse and keyboard input')
assert.doesNotMatch(source, /rfb\.viewOnly\s*=\s*true/, 'browser live viewer must not be read-only')
assert.match(source, /window\.close\(\)/, 'browser live viewer must close itself after a clean handoff disconnect')
assert.match(source, /new EventSource\(/, 'browser live viewer must subscribe to virtual pointer events')
assert.match(html, /id="virtual-pointer"/, 'browser live viewer must include a virtual pointer overlay element')
assert.match(style, /#virtual-pointer/, 'browser live viewer must style the virtual pointer overlay')
assert.match(style, /pointer-events:\s*none/, 'virtual pointer overlay must not intercept user input')
