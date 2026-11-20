<script setup lang="ts">
import { ChevronRight, CodeXml, Minus, Plus, Scan, PanelsTopLeft } from 'lucide-vue-next'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CodeNode } from './appCode'
import { codeLayout, featureCodeGraph, linkKey, traceLinks } from './codeGraph'
import ParticleLinks from './ParticleLinks.vue'
import type { LiveEvent, LiveTarget } from './liveActivity'
import { activitySignals, isExactTarget, latestOperation, operationLabel, operationColor } from './activityGraph'
interface Card { title: string; image?: string; captureCount: number; hasDemo: boolean; code?: CodeNode[] }
const props = defineProps<{ source: Card; feature: Card; group: string; liveEvents?: LiveEvent[]; freshEvents?: LiveEvent[]; liveConnected?: boolean; focusTarget?: LiveTarget | null }>()
const emit = defineEmits<{ source: []; preview: []; code: [node: CodeNode] }>()
const stage = ref<HTMLElement | null>(null)
const viewport = ref<HTMLElement | null>(null)
const hover = ref<string | null>(null)
const selected = ref<string | null>(null)
const active = computed(() => hover.value ?? selected.value)
const model = computed(() => featureCodeGraph(props.feature.code ?? []))
const links = computed(() => [{ from: 'screen', to: 'feature' }, ...model.value.links])
const layout = computed(() => codeLayout(model.value.nodes, links.value))
const traced = computed(() => traceLinks(links.value, active.value))
const activeNodes = computed(() => new Set(links.value.filter(e => traced.value.has(linkKey(e))).flatMap(e => [e.from, e.to])))
const signals = computed(() => activitySignals(props.liveEvents ?? [], props.freshEvents ?? [], links.value, props.group, props.feature.title, !!props.liveConnected))
const operations = computed(() => {
  const result = new Map<string, LiveEvent>()
  if (!props.liveConnected) return result
  for (const [port, node] of model.value.nodes) {
    const event = latestOperation((props.liveEvents ?? []).filter(e => (e.status === 'running' || Date.now() - Date.parse(e.at) < 60000) && isExactTarget(e, props.group, props.feature.title, node.key ?? node.id)))
    if (event) result.set(port,event)
  }
  return result
})
watch(() => props.focusTarget, async target => {
  if (!target?.key) return
  selected.value = `code:${target.key}`
  await nextTick()
  const card = [...(stage.value?.querySelectorAll<HTMLElement>('[data-port]') ?? [])].find(el => el.dataset.port === selected.value)
  card?.focus({ preventScroll: true }); card?.scrollIntoView({ block: 'nearest', inline: 'center' })
}, { immediate: true })
const shared = (port: string) => new Set(model.value.links.filter(e => e.to === port).flatMap(e => e.scopes ?? [])).size
function position(id: string) {
  const box = layout.value.positions.get(id)!
  return { left: `${box.left}px`, top: `${box.top}px`, width: `${box.width}px`, height: `${box.height}px` }
}
function eventPort(target: EventTarget | null) { return target instanceof Element ? target.closest<HTMLElement>('[data-port]')?.dataset.port ?? null : null }
const available = ref(1000)
const manualZoom = ref<number | null>(null)
const zoom = computed(() => manualZoom.value ?? (available.value < 700 ? 1 : Math.max(.6, Math.min(1, available.value / layout.value.width))))
function adjust(delta: number) { manualZoom.value = Math.max(.5, Math.min(1.25, Math.round((zoom.value + delta) * 100) / 100)) }
function fit() { manualZoom.value = Math.max(.1, Math.min(1, (available.value - 2) / layout.value.width)) }
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver(() => { available.value = viewport.value?.clientWidth ?? 1000 })
  if (viewport.value) observer.observe(viewport.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>
<template>
  <div class="flow-tools">
    <p>源码关系 <span>· 点选追踪源码关系；彩色粒子定位 Codex 操作</span></p>
    <div class="flow-zoom" role="group" aria-label="图谱缩放">
      <button type="button" aria-label="缩小图谱" :disabled="zoom <= .5" @click="adjust(-.1)"><Minus :size="14" /></button>
      <span>{{ Math.round(zoom * 100) }}%</span>
      <button type="button" aria-label="放大图谱" :disabled="zoom >= 1.25" @click="adjust(.1)"><Plus :size="14" /></button>
      <button type="button" @click="fit"><Scan :size="14" />适应视图</button>
    </div>
  </div>
  <div ref="viewport" class="flow-viewport" role="region" aria-label="功能与后端关系图，可横向滚动" tabindex="0">
    <div class="flow-size" :style="{ width: `${layout.width * zoom}px`, height: `${layout.height * zoom}px` }">
      <div ref="stage" class="flow-stage" :style="{ width: `${layout.width}px`, height: `${layout.height}px`, transform: `scale(${zoom})` }"
        @pointerover="hover = $event.pointerType === 'touch' ? null : eventPort($event.target)" @pointerleave="hover = null"
        @focusin="selected = eventPort($event.target) ?? selected" @click.self="selected = null">
        <ParticleLinks :stage="stage" :links="links" :active="active" :revision="layout" :signals="signals" @evidence="emit('code', $event)" />
        <button type="button" data-port="screen" class="flow-source" :style="position('screen')" :aria-label="`查看主界面：${source.title}`" @click="emit('source')">
          <img v-if="source.image" :src="source.image" alt="" /><span v-else class="flow-placeholder"><PanelsTopLeft :size="26" />暂无截图</span>
          <small>主界面</small><strong>{{ source.title }}</strong>
        </button>
        <button type="button" data-port="feature" class="flow-feature" :class="{ active: activeNodes.has('feature') }" :style="position('feature')" :aria-label="`查看功能：${feature.title}`" @click="emit('preview')">
          <img v-if="feature.image" :src="feature.image" alt="" /><span><small>功能</small><strong>{{ feature.title }}</strong><small>{{ feature.captureCount }} 张截图</small></span><ChevronRight :size="16" />
        </button>
        <article v-for="[port, node] in model.nodes" :key="port" class="flow-code" :class="{ active: activeNodes.has(port), selected: active === port }" :data-port="port" :style="{ ...position(port), ...(operations.has(port) ? { borderColor: operationColor(operations.get(port)!) } : {}) }"
          tabindex="0" :aria-label="`追踪关系：${node.title}`" @click="selected = port" @keydown.enter.prevent="selected = port" @keydown.space.prevent="selected = port">
          <div class="flow-kind"><span>{{ node.kind }}</span><small v-if="!operations.has(port) && shared(port) > 1">{{ shared(port) }} 个接口共用</small></div>
          <strong :title="node.title">{{ node.title }}</strong>
          <small v-if="operations.has(port)" class="node-operation" :data-status="operations.get(port)!.status" :style="{ color: operationColor(operations.get(port)!) }">● {{ operationLabel(operations.get(port)!) }}</small>
          <button type="button" class="flow-file" :aria-label="`查看源码：${node.title}`" :title="`${node.file}:${node.line}`" @click.stop="emit('code', node)" @keydown.stop>
            <CodeXml :size="14" /><span>{{ node.file.split('/').at(-1) }}:{{ node.line }}</span><ChevronRight :size="13" />
          </button>
        </article>
      </div>
    </div>
  </div>
  <p v-if="layout.width * zoom > available + 2" class="flow-scroll-hint">左右滚动查看完整关系，也可点「适应视图」。</p>
</template>
<style scoped>
.flow-tools { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 10px; padding: 14px 18px 4px; color: #d0e4f5; }
.flow-tools p { font-size: 12px; }
.flow-tools p span { color: #8ea8bf; }
.flow-zoom { display: flex; align-items: center; gap: 5px; font-size: 11px; }
.flow-zoom button { display: flex; align-items: center; gap: 5px; background: #112337; border: 1px solid #35536e; border-radius: 5px; min-height: 28px; padding: 4px 7px; }
.flow-zoom button:disabled { opacity: .4; }
.flow-zoom > span { min-width: 33px; text-align: center; }
.flow-viewport { overflow: auto; max-height: 740px; scrollbar-color: #42627f #0a1420; overscroll-behavior-inline: contain; }
.flow-size { position: relative; margin-inline: auto; }
.flow-stage { position: relative; isolation: isolate; transform-origin: top left; }
.flow-source, .flow-feature, .flow-code { position: absolute; z-index: 1; border: 1px solid #345470; border-radius: 9px; background: #101e2d; color: #eaf3ff; text-align: left; }
.flow-code { display: flex; flex-direction: column; padding: 10px 12px; cursor: pointer; }
.flow-code.active, .flow-feature.active { border-color: #659fc5; background: #142b40; }
.flow-code.selected { border-color: #a0e6f4; box-shadow: 0 0 20px #2b9fd21c; }
.flow-kind { display: flex; align-items: center; justify-content: space-between; color: #86b6d6; font-size: 10px; margin-bottom: 7px; }
.flow-kind small { font-size: 9px; color: #d5c391; }
.node-operation { display: block; font-size: 10px; margin-top: 4px; }
.flow-code > strong { font-size: 13px; line-height: 1.35; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow-wrap: anywhere; }
.flow-file { margin-top: auto; padding: 4px 0 0; display: flex; align-items: center; gap: 5px; width: 100%; min-width: 0; background: none; border: 0; color: #b6cee1; font-size: 10px; text-align: left; }
.flow-file span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; }
.flow-file svg { flex-shrink: 0; }
.flow-file:hover { color: #e4faff; }
.flow-source { display: flex; flex-direction: column; padding: 6px; border-color: #8c6d42; background: #171d24; }
.flow-source > img, .flow-placeholder { width: 100%; flex: 1; min-height: 0; object-fit: contain; background: #0a121e; border-radius: 4px; }
.flow-placeholder { display: grid; place-content: center; gap: 10px; font-size: 11px; }
.flow-source small { color: #e5c89d; padding: 8px 4px 2px; font-size: 10px; }
.flow-source strong { padding: 0 4px 4px; font-size: 14px; }
.flow-feature { display: flex; align-items: center; padding: 12px; gap: 9px; }
.flow-feature > img { width: 30px; height: 56px; object-fit: contain; border-radius: 4px; background: #070c14; }
.flow-feature > span { flex: 1; min-width: 0; }
.flow-feature strong, .flow-feature small { display: block; }
.flow-feature strong { font-size: 13px; margin: 4px 0; }
.flow-feature small { font-size: 10px; color: #9abbdb; }
.flow-feature > svg { flex-shrink: 0; }
.flow-scroll-hint { font-size: 11px; color: #9abbdb; padding: 7px 18px 12px; }
</style>
