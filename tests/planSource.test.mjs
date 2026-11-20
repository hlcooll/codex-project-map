import test from 'node:test'
import assert from 'node:assert/strict'
import { createRenderer, nextTick, ref } from 'vue'
import { usePlanSource } from '../src/usePlanSource.ts'

test('source drawer ignores delayed replies after another node, project switch, or close', async () => {
  const originalFetch = globalThis.fetch, pending = []
  globalThis.fetch = (url, options) => new Promise(resolve => pending.push({ url, options, resolve }))
  const renderer = createRenderer({
    insert() {}, remove() {}, createElement: () => ({}), createText: () => ({}), createComment: () => ({}),
    setText() {}, setElementText() {}, parentNode: () => null, nextSibling: () => null, patchProp() {},
  })
  const project = ref('first'), plan = { nodes: ['a', 'b'].map(id => ({ id, evidence: [{ file: `${id}.vue`, match: 'const value = 1', line: 1 }] })), edges: [] }
  let reader
  const app = renderer.createApp({ setup() { reader = usePlanSource(() => project.value, () => plan); return () => null } })
  app.mount({})
  const reply = (index, file) => pending[index].resolve({ ok: true, json: async () => ({ file, start: 1, lines: ['content'] }) })
  try {
    const first = reader.source('a', 0), second = reader.source('b', 0)
    assert.equal(pending[0].options.signal.aborted, true)
    const query = new URL(pending[1].url, 'http://localhost').searchParams
    assert.equal(query.get('node'), 'b'); assert.equal(query.get('proofFile'), 'b.vue')
    reply(1, 'b.vue'); await second
    reply(0, 'a.vue'); await first
    assert.equal(reader.snippet.value.file, 'b.vue')
    const closed = reader.source('a', 0)
    reader.closeSource(); reply(2, 'a.vue'); await closed
    assert.equal(reader.snippet.value, null)
    const switched = reader.source('a', 0)
    project.value = 'second'; await nextTick(); reply(3, 'a.vue'); await switched
    assert.equal(reader.snippet.value, null); assert.equal(reader.sourceLoading.value, false)
  } finally { app.unmount(); globalThis.fetch = originalFetch }
})
