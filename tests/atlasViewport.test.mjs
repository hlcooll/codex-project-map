import test from 'node:test'
import assert from 'node:assert/strict'
import { zoomScroll, fitZoom } from '../src/atlasViewport.ts'

test('zoom keeps the graph point under the pointer fixed, including an already panned canvas', () => {
  const before = { left: 600, top: 230 }, anchor = { x: 175, y: 100 }
  const after = zoomScroll(before, anchor, .85, 1.3)
  assert.equal((before.left + anchor.x) / .85, (after.left + anchor.x) / 1.3)
  assert.equal((before.top + anchor.y) / .85, (after.top + anchor.y) / 1.3)
})
test('fitting a large chain preserves the readable zoom floor instead of shrinking text to nothing', () => {
  assert.equal(fitZoom({ width: 14000, height: 1200 }, { width: 390, height: 600 }), .5)
  assert.equal(fitZoom({ width: 600, height: 400 }, { width: 1000, height: 800 }), 1)
})
