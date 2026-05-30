import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import assert from 'node:assert/strict'

const source = readFileSync(join(import.meta.dirname, 'src/main.ts'), 'utf8')

assert.match(source, /rfb\.viewOnly\s*=\s*false/, 'browser live viewer must allow mouse and keyboard input')
assert.doesNotMatch(source, /rfb\.viewOnly\s*=\s*true/, 'browser live viewer must not be read-only')
