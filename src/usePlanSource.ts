import { onBeforeUnmount, ref, watch } from 'vue'
import type { PlanDocument } from './projectPlan'

export function usePlanSource(projectId: () => string, document: () => PlanDocument | undefined) {
  const snippet = ref<{ file: string; start: number; lines: string[] } | null>(null)
  const snippetError = ref(''), sourceLoading = ref(false)
  let controller: AbortController | undefined
  let request = 0
  function closeSource() {
    request++; controller?.abort(); snippet.value = null; snippetError.value = ''; sourceLoading.value = false
  }
  async function source(node: string, index: number, edge?: number) {
    closeSource()
    const current = request, id = projectId(), plan = document()
    const relation = edge === undefined ? undefined : plan?.edges[edge]
    const proof = (relation ?? plan?.nodes.find(n => n.id === node))?.evidence[index]
    if (!proof) { snippetError.value = '这条关联尚无可读取的源码依据。'; return }
    const query = new URLSearchParams({ evidence: String(index), proofFile: proof.file, proofMatch: proof.match ?? '' })
    if (relation) {
      query.set('from', relation.from); query.set('to', relation.to); query.set('label', relation.label)
    } else query.set('node', node)
    controller = new AbortController()
    sourceLoading.value = true
    try {
      const response = await fetch(`/api/projects/${encodeURIComponent(id)}/plan-source?${query}`, { signal: controller.signal })
      if (!response.ok) throw new Error('此源码位置已变化或暂不可读，请重新核对关联。')
      const data = await response.json()
      if (current === request && id === projectId()) snippet.value = data
    } catch (cause) {
      if (current === request) snippetError.value = cause instanceof Error ? cause.message : '读取源码失败。'
    } finally { if (current === request) sourceLoading.value = false }
  }
  watch(projectId, closeSource)
  onBeforeUnmount(closeSource)
  return { snippet, snippetError, sourceLoading, source, closeSource }
}
