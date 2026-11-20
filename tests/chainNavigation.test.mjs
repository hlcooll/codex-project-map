import test from 'node:test'
import assert from 'node:assert/strict'
import { chainURL, readChainRoute } from '../src/chainNavigation.ts'

test('refreshable chain URL preserves project, source page and node IDs with reserved characters', () => {
  const route = { project: 'project/a', root: '页面/今天?状态=夜间 & #1', from: 'app-ui' }
  assert.deepEqual(readChainRoute(chainURL(route)), route)
})
test('global view has no invented root and invalid navigation cannot select a project', () => {
  assert.deepEqual(readChainRoute(chainURL({ project: 'p', root: '', from: 'workbench' })), { project: 'p', root: '', from: 'workbench' })
  for (const hash of ['', '#elsewhere', '#/chain?', '#/chain?root=n']) assert.equal(readChainRoute(hash), null)
  assert.equal(readChainRoute('#/chain?project=p&from=unknown').from, 'workbench')
})
