import { test } from 'node:test'
import assert from 'node:assert/strict'
import { activitySignals, isExactTarget, latestOperation, operationColor } from '../src/activityGraph.ts'

const target = { group: '食物', screen: '购物清单', key: 'handler', file: 'api.go', precision: 'symbol' }
const event = (changes = {}) => ({ id: 'event', version: '1', threadId: 'thread', turnId: 'turn', kind: 'write', status: 'running', at: new Date().toISOString(), title: '修改文件', detail: '', paths: ['api.go'], targets: [target], mapping: 'exact', ...changes })
const edges = [{ from: 'feature', to: 'code:api' }, { from: 'code:api', to: 'code:handler' }, { from: 'code:handler', to: 'code:db' }]
test('an exact operation illuminates only the incoming relation to its actual code target', () => {
  const signals = activitySignals([event()], [], edges, '食物', '购物清单', true)
  assert.deepEqual(signals.map(x => [x.from, x.to]), [['code:api', 'code:handler']])
  assert.equal(signals[0].repeat, true)
  assert.equal(activitySignals([event()], [], edges, '其他', '购物清单', true).length, 0)
})
test('file associations, old completed events and a disconnected source never animate', () => {
  const file = event({ mapping: 'file', targets: [{ ...target, precision: 'file' }] })
  assert.equal(isExactTarget(file, '食物', '购物清单'), false)
  assert.equal(activitySignals([file], [file], edges, '食物', '购物清单', true).length, 0)
  assert.equal(activitySignals([event({ status: 'completed' })], [], edges, '食物', '购物清单', true).length, 0)
  assert.equal(activitySignals([event()], [event()], edges, '食物', '购物清单', false).length, 0)
})
test('a fresh completion or failure produces one pulse and supersedes its running version', () => {
  const failure = event({ version: '2', status: 'failed' })
  const signals = activitySignals([event()], [failure], edges, '食物', '购物清单', true)
  assert.equal(signals.length, 1)
  assert.equal(signals[0].repeat, false)
  assert.equal(signals[0].color, operationColor(failure))
  assert.ok(signals[0].id.includes(':2:'))
  assert.equal(latestOperation([failure, event()]).status, 'running')
})
