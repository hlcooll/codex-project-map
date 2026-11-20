<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import ProjectAtlas from './ProjectAtlas.vue'
import ActivityPanel from './ActivityPanel.vue'
import { usePlanSource } from './usePlanSource'
import { operationLabel } from './activityGraph'
import { planKindLabel, type PlanEdge, type PlanState } from './projectPlan'
import type { LiveEvent, LiveSnapshot, LiveTarget } from './liveActivity'

const props = defineProps<{
  project: { id: string; name: string }; rootId: string; origin: 'app-ui' | 'workbench'
  snapshot: LiveSnapshot | null; connected: boolean; selectedThread: string | null
  events: LiveEvent[]; pulses: LiveEvent[]
}>()
const emit = defineEmits<{ close: []; root: [id: string]; selectThread: [id: string | null]; demo: [path: string] }>()
const state = ref<PlanState | null>(null), error = ref('')
const selectedId = ref(''), selectedEdge = ref<PlanEdge | null>(null), panel = ref<'node' | 'activity' | null>(null)
const atlas = ref<InstanceType<typeof ProjectAtlas> | null>(null)
const backButton = ref<HTMLButtonElement | null>(null), drawerTitle = ref<HTMLHeadingElement | null>(null)
const drawer = ref<HTMLElement | null>(null), narrowQuery = window.matchMedia('(max-width: 900px)')
const narrow = ref(narrowQuery.matches), modalDrawer = computed(() => narrow.value && !!panel.value)
let detailReturnFocus: HTMLElement | null = null
const root = computed(() => state.value?.document.nodes.find(n => n.id === props.rootId))
const selected = computed(() => state.value?.document.nodes.find(n => n.id === selectedId.value))
const missingRoot = computed(() => !!state.value && !!props.rootId && !root.value)
const edge = computed(() => state.value?.document.edges.find(e => e.from === selectedEdge.value?.from && e.to === selectedEdge.value?.to && e.label === selectedEdge.value?.label))
const edgeMissing = computed(() => !!selectedEdge.value && !edge.value)
const title = computed(() => selectedEdge.value ? (edge.value ? '关系依据' : '关系已更新') : selected.value?.title ?? '节点已不在当前档案中')
const nodeTitle = (id: string) => state.value?.document.nodes.find(n => n.id === id)?.title ?? id
const relations = computed(() => state.value?.document.edges.flatMap((item, index) =>
  item.from === selectedId.value || item.to === selectedId.value ? [{ ...item, index }] : []) ?? [])
const currentOperation = computed(() => props.connected ? props.events.find(e => e.kind !== 'message' && e.status === 'running') : undefined)
const { snippet, snippetError, sourceLoading, source, closeSource } = usePlanSource(() => props.project.id, () => state.value?.document)
let controller: AbortController | undefined, timer: number | undefined, generation = 0

watch(() => props.project.id, () => {
  const version = ++generation
  controller?.abort(); window.clearTimeout(timer); controller = new AbortController()
  state.value = null; error.value = ''; closePanel()
  const poll = async () => {
    try {
      const response = await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/workbench`, { signal: controller!.signal })
      if (!response.ok) throw new Error('无法读取项目链路，正在重试。')
      const data = await response.json() as PlanState
      if (version === generation) { state.value = data; error.value = '' }
    } catch (cause) {
      if (version === generation && !controller?.signal.aborted) error.value = cause instanceof Error ? cause.message : '连接失败。'
    } finally { if (version === generation) timer = window.setTimeout(poll, 2500) }
  }
  void poll()
}, { immediate: true })
watch(() => props.rootId, () => { selectedId.value = props.rootId; if (panel.value) dismissPanel(); else closePanel() }, { immediate: true })
function resizeDrawer() { narrow.value = narrowQuery.matches; if (modalDrawer.value) void focusDrawer() }
onMounted(() => { backButton.value?.focus(); narrowQuery.addEventListener('change', resizeDrawer) })
onBeforeUnmount(() => { generation++; controller?.abort(); window.clearTimeout(timer); narrowQuery.removeEventListener('change', resizeDrawer) })

function closePanel() { panel.value = null; selectedEdge.value = null; closeSource() }
function rememberFocus() { if (!panel.value) detailReturnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null }
function dismissPanel() {
  closePanel()
  void nextTick(() => {
    if (detailReturnFocus?.isConnected && detailReturnFocus.getClientRects().length) detailReturnFocus.focus({ preventScroll: true })
    else backButton.value?.focus({ preventScroll: true })
  })
}
function toggleActivity() { if (panel.value === 'activity') dismissPanel(); else { rememberFocus(); panel.value = 'activity'; void focusDrawer() } }
async function focusDrawer() { await nextTick(); drawerTitle.value?.focus({ preventScroll: true }) }
function inspect(id: string) {
  rememberFocus(); closeSource(); selectedId.value = id; selectedEdge.value = null; panel.value = 'node'; void focusDrawer()
}
function inspectSource(id: string, index: number) { inspect(id); void source(id, index) }
function inspectEdge(index: number) {
  rememberFocus(); closeSource(); selectedEdge.value = state.value?.document.edges[index] ?? null; panel.value = 'node'; void focusDrawer()
}
function edgeSource(index: number) {
  const edgeIndex = state.value?.document.edges.findIndex(e => e.from === edge.value?.from && e.to === edge.value?.to && e.label === edge.value?.label) ?? -1
  if (edgeIndex >= 0) void source('', index, edgeIndex)
}
async function locate(target: LiveTarget) {
  if (target.group !== '项目图谱' || !target.key) return
  inspect(target.key)
  await nextTick()
  await atlas.value?.locateNode(target.key)
}
function escape(event: KeyboardEvent) {
  if (event.key === 'Tab' && modalDrawer.value && drawer.value) {
    const focusable = [...drawer.value.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input, select, textarea, [tabindex="0"]')].filter(el => el.getClientRects().length)
    const first = focusable[0], last = focusable.at(-1)
    if (event.shiftKey && (document.activeElement === first || document.activeElement === drawerTitle.value)) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    return
  }
  if (event.key !== 'Escape' || (event.target as HTMLElement).closest('dialog')) return
  event.preventDefault()
  if (panel.value) dismissPanel(); else emit('close')
}
</script>

<template>
  <main class="chain-screen" aria-label="整屏功能链路" @keydown="escape">
    <header class="chain-header" :inert="modalDrawer">
      <button ref="backButton" type="button" class="dialog-button" @click="emit('close')">返回{{ origin === 'app-ui' ? ' App 界面' : '项目规划' }}</button>
      <div class="chain-heading"><p>{{ project.name }} <span>/ 功能链路</span></p><h1>{{ root?.title || (rootId ? '定位界面' : '全局图谱') }}</h1></div>
      <span v-if="state" class="chain-revision">{{ state.source === 'codex' ? `档案第 ${state.revision} 版` : '目录扫描' }}</span>
    </header>
    <p v-if="error" class="chain-message form-error" role="alert">{{ error }}{{ state ? ' 当前保留上次读取的图谱。' : '' }}</p>
    <div v-if="!state" class="chain-empty" role="status">{{ error ? '等待连接恢复…' : '正在读取功能链路…' }}</div>
    <div v-else-if="missingRoot" class="chain-empty"><h2>这张卡片已不在当前档案中</h2><p>分析结果可能已经更新，可以返回选择其他界面，或查看当前全局图谱。</p><button class="dialog-button" type="button" @click="emit('root','')">查看全局图谱</button></div>
    <div v-else class="chain-body">
      <div class="chain-canvas" :inert="modalDrawer"><ProjectAtlas ref="atlas" fullscreen :project-id="project.id" :document="state.document" :ui="state.ui" :root-id="rootId" :selected-id="selectedId" :events="events" :pulses="pulses" :connected="connected" @select="inspect" @root="emit('root',$event)" @source="inspectSource" @edge="inspectEdge" @demo="emit('demo',$event)" /></div>
      <aside v-if="panel" ref="drawer" class="chain-drawer" :role="modalDrawer ? 'dialog' : 'complementary'" :aria-modal="modalDrawer ? 'true' : undefined" aria-labelledby="chain-drawer-title">
        <header class="drawer-header"><h2 id="chain-drawer-title" ref="drawerTitle" tabindex="-1">{{ panel === 'activity' ? '会话记录' : title }}</h2><button class="dialog-button" type="button" @click="dismissPanel">关闭详情</button></header>
        <ActivityPanel v-if="panel === 'activity'" :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :events="events" @select-thread="emit('selectThread',$event)" @locate="locate" />
        <div v-else class="drawer-content">
          <p v-if="edgeMissing" class="drawer-note">这条关系已不在当前档案中，请从图谱重新选择。</p>
          <template v-else-if="edge">
            <p>{{ nodeTitle(edge.from) }} → {{ nodeTitle(edge.to) }}</p><h3>{{ edge.label }}</h3>
            <p class="drawer-note">{{ edge.origin === 'manifest' ? '项目映射' : edge.origin === 'scan' ? '目录结构' : 'Codex 分析' }} · 源码关系依据</p>
            <ul class="evidence-list"><li v-for="(proof,index) in edge.evidence" :key="`${proof.file}:${index}`"><button v-if="proof.line > 0" type="button" @click="edgeSource(index)">{{ proof.file }}:{{ proof.line }}</button><code v-else>{{ proof.file }}</code><small>{{ proof.valid ? '位置已核对' : proof.reason || '待确认' }}</small></li></ul>
            <p v-if="!edge.evidence.length" class="drawer-note">尚无源码依据。</p>
          </template>
          <template v-else-if="selected">
            <p class="drawer-note">{{ selected.category || planKindLabel[selected.kind] }} · {{ selected.state === 'existing' ? '已有实现 · 未代表验收' : selected.state === 'planned' ? '计划新增' : '待确认' }}</p>
            <p>{{ selected.description }}</p>
            <button class="dialog-button" type="button" :disabled="selected.id === rootId" @click="emit('root',selected.id)">以此为起点</button>
            <h3>源码依据</h3>
            <ul class="evidence-list"><li v-for="(proof,index) in selected.evidence" :key="`${proof.file}:${index}`"><button v-if="proof.line > 0" type="button" @click="source(selected!.id,index)">{{ proof.file }}:{{ proof.line }}</button><code v-else>{{ proof.file }}</code><small>{{ proof.valid ? '位置已核对' : proof.reason || '待确认' }}</small></li></ul>
            <p v-if="!selected.evidence.length" class="drawer-note">尚无源码依据。</p>
            <template v-if="selected.acceptance.length"><h3>验收要求</h3><ul><li v-for="item in selected.acceptance" :key="item">{{ item }}</li></ul></template>
          </template>
          <section v-if="snippet || snippetError || sourceLoading" class="chain-source" aria-label="源码内容">
            <p v-if="sourceLoading" role="status">正在读取源码…</p><p v-else-if="snippetError" class="form-error" role="alert">{{ snippetError }}</p>
            <template v-else-if="snippet"><h3>{{ snippet.file }}</h3><pre tabindex="0"><code v-for="(line,index) in snippet.lines" :key="index">{{ snippet.start + index }}  {{ line }}{{ '\n' }}</code></pre></template>
          </section>
          <template v-if="!selectedEdge && selected && relations.length"><h3>关联关系 · {{ relations.length }}</h3><ul class="relation-list"><li v-for="item in relations" :key="item.index"><span>{{ nodeTitle(item.from) }} → {{ nodeTitle(item.to) }}</span><button type="button" @click="inspectEdge(item.index)">{{ item.label }} · 查看依据</button></li></ul></template>
          <p v-if="!selectedEdge && !selected" class="drawer-note">节点已更新，请从图谱重新选择。</p>
        </div>
      </aside>
    </div>
    <footer class="chain-status" :inert="modalDrawer"><span :class="{ paused: !connected }">{{ !connected ? '实时状态暂停 · 粒子已停止' : currentOperation ? `${operationLabel(currentOperation)} · ${currentOperation.title}` : '已连接 · 当前无执行中的操作' }}</span><button type="button" class="dialog-button" :aria-expanded="panel === 'activity'" @click="toggleActivity">会话记录{{ selectedThread ? ' · 已筛选' : '' }}</button></footer>
  </main>
</template>

<style scoped>
.chain-screen{position:fixed;inset:0;height:100dvh;z-index:20;display:flex;flex-direction:column;background:#091421;color:#dcebf4;font-size:16px}
.chain-header{display:flex;align-items:center;gap:22px;padding:14px 22px;background:#0c1b22;border-bottom:1px solid #29414e;flex-shrink:0}
.chain-header button,.chain-screen .dialog-button{font-size:14px;min-height:36px}
.chain-heading{min-width:0;flex:1}.chain-heading p{font-size:13px;color:#a9c2cb;overflow-wrap:anywhere}.chain-heading p span{color:#7796a6}.chain-heading h1{margin:2px 0 0;font-size:20px;font-weight:600;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.chain-revision{font-size:13px;color:#9cb8c5;white-space:nowrap}
.chain-body{display:flex;flex:1;min-width:0;min-height:0;position:relative}.chain-body>:first-child{flex:1;min-width:0;min-height:0}
.chain-canvas{display:flex}.chain-canvas>:first-child{flex:1;min-width:0;min-height:0}
.chain-empty{flex:1;min-height:0;display:flex;flex-direction:column;gap:16px;align-items:center;justify-content:center;padding:24px;text-align:center}.chain-empty h2{font-size:20px}.chain-empty p{color:#a9c2cb}
.chain-message{padding:8px 22px;font-size:14px;background:#261d1c}
.chain-drawer{width:390px;max-width:90vw;flex-shrink:0;border-left:1px solid #345265;background:#0d1b28;overflow:auto;overscroll-behavior:contain;scrollbar-width:thin}
.drawer-header{display:flex;align-items:flex-start;gap:12px;justify-content:space-between;padding:16px;position:sticky;top:0;background:#0d1b28;z-index:2;border-bottom:1px solid #29414e}.drawer-header h2{font-size:17px;overflow-wrap:anywhere;line-height:1.5}.drawer-header button{flex-shrink:0}
.drawer-content{padding:18px;display:flex;flex-direction:column;gap:14px;font-size:14px;line-height:1.75;overflow-wrap:anywhere}.drawer-content h3{margin:6px 0 0;font-size:15px}.drawer-content ul{margin:0;padding-left:20px}.drawer-note{color:#9bb4c6}.drawer-content>.dialog-button{align-self:flex-start}.evidence-list,.relation-list{list-style:none!important;padding:0!important}.evidence-list li,.relation-list li{display:flex;flex-direction:column;gap:4px;padding:10px 0;border-bottom:1px solid #24394a}.drawer-content button:not(.dialog-button){border:0;background:none;padding:0;color:#91d6f4;text-align:left;font-size:14px;overflow-wrap:anywhere}.evidence-list small{color:#9bb4c6;font-size:12px}.chain-source{border-top:1px solid #315163;padding-top:12px}.chain-source pre{background:#07121e;max-height:48vh;overflow:auto;padding:12px;font-size:13px;white-space:pre;line-height:1.8}.chain-drawer :deep(.activity-panel){border:0;border-radius:0}.chain-drawer :deep(.activity-events){max-height:none}.chain-drawer :deep(.event-message),.chain-drawer :deep(.event-heading){font-size:14px}
.chain-status{display:flex;justify-content:space-between;align-items:center;gap:16px;padding:8px 22px;border-top:1px solid #29414e;background:#0c1b22;font-size:13px;color:#9edac7;flex-shrink:0}.chain-status>span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.chain-status .paused{color:#dcbd8b}.chain-status button{flex-shrink:0}
@media(max-width:900px){.chain-drawer{position:absolute;top:0;right:0;bottom:0;z-index:4;box-shadow:-12px 0 35px #0005}}
@media(max-width:600px){.chain-header{padding:10px 12px;gap:10px}.chain-heading h1{font-size:17px}.chain-header .dialog-button{padding:6px 8px}.chain-revision{display:none}.chain-status{padding:8px 12px;gap:8px}.chain-status>span{font-size:12px}.chain-drawer{max-width:100%;width:100%}}
</style>
