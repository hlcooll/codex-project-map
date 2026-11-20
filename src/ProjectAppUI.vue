<script setup lang="ts">
import { ChevronRight, X } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import AppFeatureMap from './AppFeatureMap.vue'
import UIPreviewManager from './UIPreviewManager.vue'
import {previewKindLabel,previewSourceLabel,type ProjectUI,type PlanCapture} from './projectPlan'
import ActivityPanel from './ActivityPanel.vue'
import type { LiveEvent, LiveSnapshot, LiveTarget } from './liveActivity'
import type { CodeLocation, CodeNode } from './appCode'

type Capture = PlanCapture
interface Screen { nodeId?:string; title: string; source?: string; demo?: string; captures?: Capture[]; code?: CodeNode[] }
interface ScreenGroup { title: string; primary?: boolean; screens: Screen[] }
interface Gallery { title: string; note?: string; groups: ScreenGroup[];ui?:ProjectUI }

const props = defineProps<{ project: { id: string; name: string }; snapshot: LiveSnapshot | null; connected: boolean; selectedThread: string | null; visibleEvents: LiveEvent[]; pulses: LiveEvent[]; externalTarget?: LiveTarget | null }>()
const emit = defineEmits<{ demo: [path: string]; selectThread: [id: string | null]; planTarget: [target: LiveTarget] }>()
const focusTarget = ref<LiveTarget | null>(null)
const followActivity = ref(false)
const gallery = ref<Gallery | null>(null)
const loading = ref(false)
const reloadToken=ref(0)
function viewChain(screen:Screen){if(!screen.nodeId)return;dialog.value?.close();emit('planTarget',{group:'项目图谱',screen:screen.title,key:screen.nodeId,file:screen.source??'',precision:'file'})}
const error = ref('')
const mode = ref<'light' | 'dark'>('light')
const dialog = ref<HTMLDialogElement | null>(null)
const activeScreen = ref<Screen | null>(null)
const captureIndex = ref(0)
const activeCapture = computed(() => activeScreen.value?.captures?.[captureIndex.value])
const groups = computed(() => (gallery.value?.groups ?? []).map((group, index) => {
  const main = group.primary ? group.screens[0] : undefined
  const features = main ? group.screens.slice(1) : group.screens
  return {
    id: `app-ui-group-${index + 1}`,
    title: group.title,
    screens: group.screens,
    main,
    features,
  }
}))
const primary = computed(() => groups.value.flatMap(group => group.main ? [{ ...group, main: group.main }] : []))
const selectedGroupId = ref('')
const branchGroup = computed(() => primary.value.find(group => group.id === selectedGroupId.value) ?? primary.value[0])
const diagram = computed(() => {
  const group = branchGroup.value
  if (group) return { source: cardFor(group.main), branches: group.features.map(cardFor) }
})

function cardFor(screen: Screen) {
  return { title: screen.title, image: captureFor(screen)?.url, captureCount: screen.captures?.length ?? 0, hasDemo: Boolean(screen.demo), code: screen.code }
}

function previewSource() {
  if (branchGroup.value) void showScreen(branchGroup.value.main)
}

function previewFeature(index: number) {
  const screen = branchGroup.value?.features[index]
  if (screen) void showScreen(screen)
}

function locateActivity(target: LiveTarget) {
  if(target.group==='项目图谱') { emit('planTarget',target); return }
  const group = groups.value.find(g => g.title === target.group)
  if (!group) return
  if (group.main && group.features.some(s => s.title === target.screen)) {
    selectedGroupId.value = group.id
    focusTarget.value = { ...target }
    return
  }
  const screen = [...group.features, ...(group.main ? [group.main] : [])].find(s => s.title === target.screen)
  const nodes = screen?.code?.flatMap(n => [n, ...(n.graph?.nodes ?? []), ...(n.uses ?? [])]) ?? []
  const node = nodes.find(n => (n.key ?? n.id) === target.key)
  if (node) void showCode(node)
  else if (screen) void showScreen(screen)
}
let followedEvent = ''
watch(() => props.pulses, events => {
  if (!followActivity.value) return
  const event = [...events].reverse().find(e => e.mapping === 'exact' && e.targets.some(t => t.precision === 'symbol' && t.key))
  if (!event || followedEvent === `${event.id}:${event.version}`) return
  followedEvent = `${event.id}:${event.version}`
  const target = event.targets.find(t => t.precision === 'symbol' && t.key)
  if (target) locateActivity(target)
})
function groupActivity(title: string) {
  return props.connected && props.visibleEvents.some(e => e.status === 'running' && e.mapping === 'exact' && e.targets.some(t => t.group === title && t.precision === 'symbol'))
}
watch([() => props.externalTarget, gallery], ([target]) => { if(target) locateActivity(target) })

const hasAppearances = computed(() => gallery.value?.groups.some(group => group.screens.some(screen => screen.captures?.some(capture => capture.mode === 'dark'))))
const picturedCount = computed(() => gallery.value?.groups.flatMap(group => group.screens).filter(screen => screen.captures?.length).length ?? 0)
const screenCount = computed(() => gallery.value?.groups.reduce((total, group) => total + group.screens.length, 0) ?? 0)

function captureFor(screen: Screen) {
  return screen.captures?.find(capture => capture.mode === mode.value) ?? screen.captures?.[0]
}

async function showScreen(screen: Screen) {
  activeScreen.value = screen
  captureIndex.value = Math.max(0, screen.captures?.findIndex(capture => capture === captureFor(screen)) ?? 0)
  await nextTick()
  dialog.value?.showModal()
}

const sourceDialog = ref<HTMLDialogElement | null>(null)
const sourceNode = ref<CodeNode | null>(null)
const sourceLocation = ref<CodeLocation | null>(null)
const snippet = ref<{ start: number; lines: string[] } | null>(null)
const snippetError = ref('')
let sourceRequest: AbortController | undefined

async function loadSnippet(location: CodeLocation) {
  sourceRequest?.abort()
  const request = new AbortController()
  sourceRequest = request
  sourceLocation.value = location
  snippet.value = null
  snippetError.value = ''
  try {
    const response = await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/app-code/${location.id}`, { signal: request.signal })
    if (!response.ok) throw new Error('源码位置已变化，请重新打开项目刷新图谱。')
    const result = await response.json() as { start: number; lines: string[] }
    if (!request.signal.aborted) snippet.value = result
  } catch (cause) {
    if (!request.signal.aborted) snippetError.value = cause instanceof Error ? cause.message : '读取源码失败。'
  }
}

async function showCode(node: CodeNode) {
  sourceNode.value = node
  void loadSnippet(node)
  await nextTick()
  sourceDialog.value?.showModal()
}

function openDemo(path: string) {
  dialog.value?.close()
  emit('demo', path)
}

watch([() => props.project.id,reloadToken], async ([id], _, onCleanup) => {
  const request = new AbortController()
  onCleanup(() => { request.abort(); sourceRequest?.abort() })
  gallery.value = null
  loading.value = true
  error.value = ''
  activeScreen.value = null
  dialog.value?.close()
  sourceDialog.value?.close()
  sourceNode.value = null
  sourceLocation.value = null
  snippet.value = null
  snippetError.value = ''
  mode.value = 'light'
  selectedGroupId.value = ''
  focusTarget.value = null
  followedEvent = ''
  try {
    const response = await fetch(`/api/projects/${encodeURIComponent(id)}/app-ui`, { signal: request.signal })
    if (!response.ok) throw new Error('无法读取 App 界面。请检查项目路径和本地服务。')
    const result = await response.json() as Gallery
    if (!request.signal.aborted) gallery.value = result
  } catch (cause) {
    if (!request.signal.aborted) error.value = cause instanceof Error ? cause.message : '读取界面失败。'
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}, { immediate: true })
</script>

<template>
  <div class="project-scan app-ui">
    <header class="scan-header">
      <div>
        <h2>{{ gallery?.title ?? project.name }}</h2>
        <p v-if="gallery?.note" class="app-ui-note">{{ gallery.note }}</p>
      </div>
      <div v-if="hasAppearances" class="app-ui-modes" role="group" aria-label="截图外观">
        <button class="dialog-button" type="button" :aria-pressed="mode === 'light'" @click="mode = 'light'">浅色</button>
        <button class="dialog-button" type="button" :aria-pressed="mode === 'dark'" @click="mode = 'dark'">深色</button>
      </div>
    </header>

    <p v-if="loading" class="scan-feedback" role="status">正在读取 App 界面…</p>
    <p v-else-if="error" class="scan-feedback form-error" role="alert">{{ error }}</p>
    <p v-else-if="!screenCount" class="scan-feedback">还没有识别出页面。请在「项目规划」执行完整分析；有页面但缺少截图时，可关联项目素材或上传原生截图。</p>
    <template v-else>
      <p v-if="gallery?.ui" class="ui-page-coverage">已识别 {{gallery.ui.total}} 个页面／界面状态 · 已关联预览 {{gallery.ui.withPreview}}/{{gallery.ui.total}} · 完整界面截图 {{gallery.ui.withCapture}}/{{gallery.ui.total}}</p><p v-for="warning in gallery?.ui?.warnings" :key="warning" class="app-ui-note">{{warning}}</p>
      <section v-if="branchGroup && diagram" class="app-ui-section" aria-labelledby="primary-screens">
        <div class="app-ui-heading">
          <h3 id="primary-screens">主界面与功能</h3>
          <span>{{ diagram.source.title }} · {{ diagram.branches.length }} 项关联功能</span>
        </div>
        <div v-if="primary.length > 1" class="app-ui-main-picker" role="group" aria-label="切换主界面">
          <button v-for="group in primary" :key="group.id" type="button" class="dialog-button"
            :aria-pressed="branchGroup.id === group.id" aria-controls="app-feature-diagram" @click="selectedGroupId = group.id">
            {{ group.main.title }}<span v-if="groupActivity(group.title)" class="group-operation"> · 操作中</span>
          </button>
        </div>
        <label class="activity-follow"><input v-model="followActivity" type="checkbox" />跟随新操作定位卡片</label>
        <div class="app-live-layout">
          <AppFeatureMap :key="branchGroup.id" :source="diagram.source" :branches="diagram.branches" :group="branchGroup.title" :live-events="visibleEvents" :fresh-events="pulses" :live-connected="connected" :focus-target="focusTarget" @source="previewSource" @preview="previewFeature" @code="showCode" />
          <ActivityPanel :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :events="visibleEvents" @select-thread="emit('selectThread',$event)" @locate="locateActivity" />
        </div>
      </section>



      <section class="app-ui-section" aria-labelledby="child-screens">
        <div class="app-ui-heading">
          <h3 id="child-screens">全部界面</h3>
          <span>{{ picturedCount }} / {{ screenCount }} 个界面有预览</span>
        </div>
        <nav v-if="groups.length > 1" class="app-ui-index" aria-label="界面目录">
          <a v-for="group in groups" :key="group.id" :href="`#${group.id}`">{{ group.title }}</a>
        </nav>
        <div v-for="group in groups" :key="group.id" class="app-ui-group">
          <h4 :id="group.id" tabindex="-1">{{ group.title }}</h4>
          <div class="app-ui-grid">
            <article v-for="screen in group.screens" :key="screen.nodeId??screen.title" class="app-screen">
              <button type="button" class="screen-open" :aria-label="`查看${screen.title}界面`" @click="showScreen(screen)"><img v-if="captureFor(screen)" :src="captureFor(screen)?.url" :alt="`${screen.title} App 界面`" loading="lazy" /><span v-else class="screen-missing">页面已识别<br/>预览待补</span><span class="app-screen-title"><strong>{{ screen.title }}</strong><ChevronRight :size="15" aria-hidden="true" /></span></button>
              <small v-if="captureFor(screen)" class="screen-kind">{{previewKindLabel[captureFor(screen)?.kind??'reference']}} · {{previewSourceLabel(captureFor(screen)?.source)}}</small>
              <div class="screen-actions"><button v-if="screen.nodeId" type="button" class="dialog-button" @click="viewChain(screen)">查看功能链路</button><UIPreviewManager v-if="screen.nodeId" :project-id="project.id" :node-id="screen.nodeId" :title="screen.title" :assets="gallery?.ui?.assets" :captures="screen.captures" @changed="reloadToken++"/></div>
            </article>
          </div>
        </div>
      </section>
      <details v-if="!branchGroup" class="ui-activity-details"><summary>Codex 会话与操作</summary><ActivityPanel :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :events="visibleEvents" @select-thread="emit('selectThread',$event)" @locate="locateActivity" /></details>
    </template>

    <dialog ref="sourceDialog" class="screen-dialog code-dialog" aria-labelledby="source-dialog-title" @close="sourceRequest?.abort()">
      <template v-if="sourceNode && sourceLocation">
        <header class="screen-dialog-header">
          <div><h3 id="source-dialog-title">{{ sourceNode.title }}</h3><code>{{ sourceLocation.file }}:{{ sourceLocation.line }}</code></div>
          <button type="button" class="dialog-button" aria-label="关闭源码预览" @click="sourceDialog?.close()"><X :size="17" aria-hidden="true" /></button>
        </header>
        <div v-if="sourceNode.evidence?.length" class="code-evidence" role="group" aria-label="关系依据">
          <button v-for="location in [sourceNode, ...sourceNode.evidence]" :key="location.id" class="dialog-button" type="button"
            :aria-pressed="sourceLocation.id === location.id" :title="location.file" @click="loadSnippet(location)">
            {{ location.id === sourceNode.id ? '实现' : '关系依据' }} · {{ location.file.split('/').at(-1) }}:{{ location.line }}
          </button>
        </div>
        <pre v-if="snippet" class="code-snippet" tabindex="0" aria-label="源码片段"><code v-for="(line, index) in snippet.lines" :key="index" :class="{ 'marked': snippet.start + index === sourceLocation.line }"><span aria-hidden="true">{{ snippet.start + index }}</span>{{ line }}{{ '\n' }}</code></pre>
        <p v-else-if="snippetError" class="scan-feedback form-error" role="alert">{{ snippetError }}</p>
        <p v-else class="scan-feedback" role="status">正在读取源码…</p>
      </template>
    </dialog>

    <dialog ref="dialog" class="screen-dialog" aria-labelledby="screen-dialog-title">
      <template v-if="activeScreen">
        <header class="screen-dialog-header">
          <div><h3 id="screen-dialog-title">{{ activeScreen.title }}</h3><code v-if="activeScreen.source">{{ activeScreen.source }}</code></div>
          <button class="dialog-button" type="button" aria-label="关闭界面预览" @click="dialog?.close()"><X :size="17" aria-hidden="true" /></button>
        </header>
        <div class="screen-dialog-tools">
          <div v-if="(activeScreen.captures?.length ?? 0) > 1" class="app-ui-modes" role="group" aria-label="界面状态">
            <button v-for="(capture, index) in activeScreen.captures" :key="capture.url" class="dialog-button" type="button" :aria-pressed="captureIndex === index" @click="captureIndex = index">{{ capture.label }}</button>
          </div>
          <button v-if="activeScreen.nodeId" class="dialog-button" type="button" @click="viewChain(activeScreen)">查看功能链路</button><button v-if="activeScreen.demo" class="dialog-button" type="button" @click="openDemo(activeScreen.demo)">试玩相关 Demo</button>
        </div>
        <p v-if="activeCapture" class="app-ui-note">{{previewKindLabel[activeCapture.kind??'reference']}} · {{previewSourceLabel(activeCapture.source)}} · {{activeCapture.reason}}</p>
        <a v-if="activeCapture" class="screen-dialog-image" :href="activeCapture.url" target="_blank" rel="noopener noreferrer" aria-label="在新标签页打开原图">
          <img :src="activeCapture.url" :alt="`${activeScreen.title} · ${activeCapture.label}`" />
        </a>
        <p v-else class="scan-feedback">该界面暂无截图。</p>
      </template>
    </dialog>
  </div>
</template>

<style scoped>
.ui-page-coverage{font-size:13px;line-height:1.8;color:#bddccb;margin-bottom:20px}.screen-open{border:0;background:none;color:inherit;padding:0;text-align:left;cursor:pointer}.screen-missing{display:grid;place-content:center;aspect-ratio:9/19.6;text-align:center;font-size:12px;color:#9fb8ab;background:#0b1912;border-radius:7px}.screen-kind{font-size:10px;color:#aac8b6;margin:6px 3px}.screen-actions{display:flex;flex-wrap:wrap;gap:5px;margin-top:auto;padding:6px 0}.screen-actions .dialog-button{font-size:11px;padding:4px 7px}.ui-activity-details{margin-top:20px}.ui-activity-details summary{cursor:pointer;margin-bottom:12px;font-size:13px}

.app-live-layout { display: grid; grid-template-columns: minmax(0, 1fr) 330px; gap: 16px; align-items: start; }
.app-live-layout > * { min-width: 0; }
.activity-follow { display: flex; align-items: center; gap: 7px; width: fit-content; margin: 10px 0 14px; font-size: 12px; color: #b2c8d9; }
.activity-follow input { accent-color: #71cbe8; }
.group-operation { color: #91defb; font-size: 11px; }
@media (max-width: 1200px) { .app-live-layout { grid-template-columns: minmax(0, 1fr); } }
</style>
