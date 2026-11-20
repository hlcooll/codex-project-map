import { test } from 'node:test'
import assert from 'node:assert/strict'
import { codeLayout, featureCodeGraph, linkKey, traceLinks } from '../src/codeGraph.ts'
const node = (key, kind = '后端组件') => ({ key, id: key, title: key, kind, file: 'api.go', line: 1 })
function root(key) {
  const value = node(key, '后端接口')
  value.graph = {
    nodes: [node(key), node('auth', '中间件'), node(`handler-${key}`), node('db', '数据存储')],
    edges: [ { from: key, to: 'auth', label: '进入鉴权' }, { from: 'auth', to: `handler-${key}`, label: '鉴权通过' }, { from: `handler-${key}`, to: 'db', label: '读取' } ],
  }
  return value
}
test('shared middleware and database use one node without mixing route traces', () => {
  const graph = featureCodeGraph([root('get'), root('patch')])
  assert.equal(graph.nodes.size, 6)
  const traced = traceLinks(graph.links, 'code:handler-get')
  const edges = graph.links.filter(e => traced.has(linkKey(e)))
  assert.ok(edges.some(e => e.from === 'code:get' && e.to === 'code:auth'))
  assert.ok(edges.some(e => e.from === 'code:handler-get' && e.to === 'code:db'))
  assert.ok(edges.every(e => !e.from.includes('patch') && !e.to.includes('patch')))
  const all = traceLinks(graph.links, 'code:auth')
  assert.ok(graph.links.filter(e => all.has(linkKey(e))).some(e => e.to === 'code:handler-patch'))
})
test('all source and backend nodes remain visible in dependency order with no overlaps', () => {
  const graph = featureCodeGraph([root('get'), root('patch'), node('view', 'UI 页面')])
  const links = [{ from: 'screen', to: 'feature' }, ...graph.links]
  const layout = codeLayout(graph.nodes, links)
  assert.equal(layout.positions.size, graph.nodes.size + 2)
  for (const edge of links) {
    const a = layout.positions.get(edge.from), b = layout.positions.get(edge.to)
    assert.ok(b.left >= a.left + a.width, `${edge.from} should precede ${edge.to}`)
  }
  const boxes = [...layout.positions.values()]
  boxes.forEach((a, i) => boxes.slice(i + 1).forEach(b => {
    assert.ok(a.left + a.width <= b.left || b.left + b.width <= a.left || a.top + a.height <= b.top || b.top + b.height <= a.top)
  }))
})
test('old manifests stay explicit dependencies and UI-only features stay client-only', () => {
  const legacy = { ...node('api', '后端接口'), uses: [node('old')] }
  assert.equal(featureCodeGraph([legacy]).links.at(-1).label, '依赖')
  const graph = featureCodeGraph([node('weather', '客户端服务')])
  assert.equal(graph.nodes.size, 1)
  assert.deepEqual(graph.links.map(e => e.from), ['feature'])
})
test('merging individually acyclic routes preserves opposite relations without overlapping cards', () => {
  const a = node('route-a', '后端接口'), b = node('route-b', '后端接口')
  a.graph = { nodes: [a, node('shared-a'), node('shared-b')], edges: [{from:a.key,to:'shared-a',label:'调用'},{from:'shared-a',to:'shared-b',label:'调用'}] }
  b.graph = { nodes: [b, node('shared-a'), node('shared-b')], edges: [{from:b.key,to:'shared-b',label:'调用'},{from:'shared-b',to:'shared-a',label:'调用'}] }
  const graph = featureCodeGraph([a,b])
  const layout = codeLayout(graph.nodes, [{from:'screen',to:'feature'},...graph.links])
  assert.notEqual(layout.positions.get('code:shared-a').left, layout.positions.get('code:shared-b').left)
  assert.ok(graph.links.some(e=>e.from==='code:shared-a' && e.to==='code:shared-b'))
  assert.ok(graph.links.some(e=>e.from==='code:shared-b' && e.to==='code:shared-a'))
  assert.ok([...layout.positions.values()].every(p=>Number.isFinite(p.left)))
})
test('shared relations retain all distinct source evidence', () => {
  const a=root('get'), b=root('patch')
  const evidence = id => ({id,file:'api.go',line:Number(id)})
  a.graph.edges.push({from:'auth',to:'db',label:'读取',evidence:[evidence('1')]})
  b.graph.edges.push({from:'auth',to:'db',label:'读取',evidence:[evidence('1'),evidence('2')]})
  const graph=featureCodeGraph([a,b])
  const shared=graph.links.find(e=>e.from==='code:auth' && e.to==='code:db')
  assert.deepEqual(shared.evidence.map(e=>e.id),['1','2'])
})
