export type ChainOrigin = 'app-ui' | 'workbench'
export interface ChainRoute { project: string; root: string; from: ChainOrigin }

// Hash navigation also works with the local Go server's static-file handler.
export function readChainRoute(hash: string): ChainRoute | null {
  if (!hash.startsWith('#/chain?')) return null
  const query = new URLSearchParams(hash.slice('#/chain?'.length))
  const project = query.get('project') ?? ''
  if (!project) return null
  return { project, root: query.get('root') ?? '', from: query.get('from') === 'app-ui' ? 'app-ui' : 'workbench' }
}

export function chainURL(route: ChainRoute): string {
  return '#/chain?' + new URLSearchParams({ project: route.project, root: route.root, from: route.from })
}
