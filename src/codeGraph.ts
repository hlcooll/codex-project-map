import type { CodeNode, CodeLocation } from './appCode'

export interface GraphLink {
  from: string
  to: string
  label?: string
  scopes?: string[]
  evidence?: CodeLocation[]
}
export const linkKey = (edge: GraphLink) => `${edge.from}\u0000${edge.to}\u0000${edge.label ?? ''}`
export const codePort = (node: CodeNode) => `code:${node.key ?? node.id}`

// Preserve endpoint context when a middleware node is shared by multiple routes.
export function traceLinks(links: GraphLink[], port: string | null) {
  const selected = new Set<string>()
  if (!port) return selected
  const scopes = new Set(links.filter(e => e.from === port || e.to === port).flatMap(e => e.scopes ?? []))
  const allowed = links.filter(e => !e.scopes?.length || e.scopes.some(scope => scopes.has(scope)))
  for (const direction of ['from', 'to'] as const) {
    const visited = new Set<string>()
    const visit = (node: string) => {
      if (visited.has(node)) return
      visited.add(node)
      for (const edge of allowed) {
        if (edge[direction] !== node) continue
        selected.add(linkKey(edge))
        visit(edge[direction === 'from' ? 'to' : 'from'])
      }
    }
    visit(port)
  }
  return selected
}

export function featureCodeGraph(roots: readonly CodeNode[]) {
  const nodes = new Map<string, CodeNode>()
  const edges = new Map<string, GraphLink>()
  function add(edge: GraphLink) {
    const key = linkKey(edge), previous = edges.get(key)
    if (previous) {
      previous.scopes = [...new Set([...(previous.scopes ?? []), ...(edge.scopes ?? [])])]
      previous.evidence = [...new Map([...(previous.evidence ?? []), ...(edge.evidence ?? [])].map(location => [location.id, location])).values()]
    }
    else edges.set(key, edge)
  }
  for (const root of roots) {
    const port = codePort(root)
    nodes.set(port, root)
    add({ from: 'feature', to: port, scopes: [port] })
    if (root.graph) {
      for (const node of root.graph.nodes) {
        if (!nodes.has(codePort(node))) nodes.set(codePort(node), node)
      }
      for (const relation of root.graph.edges) {
        add({ ...relation, from: `code:${relation.from}`, to: `code:${relation.to}`, scopes: [port] })
      }
    } else {
      for (const dep of root.uses ?? []) {
        nodes.set(codePort(dep), dep)
        add({ from: port, to: codePort(dep), label: '依赖', scopes: [port] })
      }
    }
  }
  return { nodes, links: [...edges.values()].filter(e => nodes.has(e.to) && (e.from === 'feature' || nodes.has(e.from))) }
}

// Layer by real edges, not by the node's category. Shared dependencies occupy one card.
export function codeLayout(nodes: Map<string, CodeNode>, links: GraphLink[]) {
  const ids = ['screen', 'feature', ...nodes.keys()]
  // Individually acyclic endpoint graphs can form cycles when common nodes are
  // merged. Condense those groups first, then give every member its own column;
  // the renderer retains reverse relations in the outer connector gutter.
  const adjacency = new Map(ids.map(id => [id, links.filter(e => e.from === id).map(e => e.to)]))
  const number = new Map<string, number>(), low = new Map<string, number>()
  const stack: string[] = [], onStack = new Set<string>(), groups: string[][] = []
  let sequence = 0
  function visit(id: string) {
    number.set(id, sequence); low.set(id, sequence++); stack.push(id); onStack.add(id)
    for (const next of adjacency.get(id) ?? []) {
      if (!number.has(next)) { visit(next); low.set(id, Math.min(low.get(id)!, low.get(next)!)) }
      else if (onStack.has(next)) low.set(id, Math.min(low.get(id)!, number.get(next)!))
    }
    if (low.get(id) === number.get(id)) {
      const group: string[] = []
      let next: string
      do { next = stack.pop()!; onStack.delete(next); group.push(next) } while (next !== id)
      groups.push(group.sort((a, b) => ids.indexOf(a) - ids.indexOf(b)))
    }
  }
  ids.forEach(id => { if (!number.has(id)) visit(id) })
  const membership = new Map(groups.flatMap((group, index) => group.map(id => [id, index] as const)))
  const transitions = [...new Map(links.map(e => [membership.get(e.from)!, membership.get(e.to)!] as const).filter(([a, b]) => a !== b).map(pair => [pair.join(':'), pair])).values()]
  const starts = groups.map(() => 0)
  const indegree = groups.map((_, index) => transitions.filter(([, to]) => to === index).length)
  const queue = groups.flatMap((_, index) => indegree[index] === 0 ? [index] : [])
  for (let i = 0; i < queue.length; i++) {
    const group = queue[i]!
    for (const [, next] of transitions.filter(([from]) => from === group)) {
      starts[next] = Math.max(starts[next]!, starts[group]! + groups[group]!.length)
      if (--indegree[next]! === 0) queue.push(next)
    }
  }
  const ranks = new Map(groups.flatMap((group, index) => group.map((id, offset) => [id, starts[index]! + offset] as const)))
  const columns: string[][] = []
  for (const id of ids) (columns[ranks.get(id) ?? 0] ??= []).push(id)
  const heightFor = (id: string) => id === 'screen' ? 378 : id === 'feature' ? 92 : 112
  const widthFor = (rank: number) => rank === 0 ? 154 : rank === 1 ? 174 : 220
  const colHeight = (col: string[]) => col.reduce((sum, id) => sum + heightFor(id) + 22, -22)
  const height = Math.max(410, ...columns.map(colHeight)) + 52
  const positions = new Map<string, { left: number; top: number; width: number; height: number }>()
  let x = 24
  columns.forEach((col, rank) => {
    let y = (height - colHeight(col)) / 2
    for (const id of col) {
      positions.set(id, { left: x, top: y, width: widthFor(rank), height: heightFor(id) })
      y += heightFor(id) + 22
    }
    x += widthFor(rank) + 90
  })
  return { positions, width: x - 66, height }
}
