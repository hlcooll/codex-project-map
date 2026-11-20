<script setup lang="ts">
import { ChevronRight, GitFork, PanelsTopLeft } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import CodeFlowMap from './CodeFlowMap.vue'
import ParticleLinks from './ParticleLinks.vue'
import type { CodeNode } from './appCode'
import type { LiveEvent, LiveTarget } from './liveActivity'
import { isExactTarget, latestOperation, operationColor, operationLabel, type ActivitySignal } from './activityGraph'
interface Card { title: string; image?: string; captureCount: number; hasDemo: boolean; code?: CodeNode[] }
const props = defineProps<{ source: Card; branches: readonly Card[]; group: string; liveEvents?: LiveEvent[]; freshEvents?: LiveEvent[]; liveConnected?: boolean; focusTarget?: LiveTarget | null }>()
const emit = defineEmits<{ source: []; preview: [index: number]; code: [node: CodeNode] }>()
const stage = ref<HTMLElement | null>(null)
const feature = ref<number | null>(null)
const hover = ref<string | null>(null)
const focus = ref<string | null>(null)
const current = computed(() => hover.value ?? focus.value)
const selectedFeature = computed(() => feature.value === null ? null : props.branches[feature.value])
const links = computed(() => props.branches.map((_, index) => ({ from: 'screen', to: `feature-${index}` })))
const operations = computed(() => props.branches.map(card => props.liveConnected ? latestOperation((props.liveEvents ?? []).filter(e => (e.status === 'running' || Date.now() - Date.parse(e.at) < 60000) && isExactTarget(e,props.group,card.title))) : undefined))
const signals = computed(() => {
  if (!props.liveConnected) return []
  const events = new Map((props.liveEvents ?? []).filter(e => e.status === 'running').map(e => [e.id,e]))
  for (const e of props.freshEvents ?? []) if (['running','completed','failed'].includes(e.status)) events.set(e.id,e)
  return [...events.values()].flatMap(e => props.branches.flatMap((card,index):ActivitySignal[] => isExactTarget(e,props.group,card.title) ? [{ id: `${e.id}:${e.version}:${index}`, from:'screen',to:`feature-${index}`,color:operationColor(e),repeat:e.status==='running' }] : []))
})
watch(() => props.focusTarget, target => {
  if (!target || target.group !== props.group) return
  const index = props.branches.findIndex(card => card.title === target.screen)
  if (index >= 0) void chooseFeature(index)
}, { immediate: true })
function eventPort(target: EventTarget | null) { return target instanceof Element ? target.closest<HTMLElement>('[data-port]')?.dataset.port ?? null : null }
async function chooseFeature(index: number | null) {
  const previous = feature.value
  feature.value = index; hover.value = focus.value = null
  await nextTick()
  if (index === null && previous !== null) stage.value?.querySelector<HTMLElement>(`[data-port="feature-${previous}"] .feature-toggle`)?.focus({ preventScroll: true })
}
function details(card: Card) { return `${card.captureCount ? `${card.captureCount} 张截图` : '暂无截图'}${card.hasDemo ? ' · 可试玩 Demo' : ''}` }
</script>
<template>
  <div id="app-feature-diagram" class="feature-map">
    <template v-if="selectedFeature && feature !== null">
      <div class="code-toolbar"><button type="button" class="code-reset" @click="chooseFeature(null)">‹ 返回全部 {{ branches.length }} 项功能</button></div>
      <CodeFlowMap :source="source" :feature="selectedFeature" :group="group" :live-events="liveEvents" :fresh-events="freshEvents" :live-connected="liveConnected" :focus-target="focusTarget" @source="emit('source')" @preview="emit('preview', feature!)" @code="emit('code', $event)" />
    </template>
    <div v-else ref="stage" class="feature-stage" :class="{ 'has-branches': branches.length }"
      @pointerover="hover = $event.pointerType === 'touch' ? null : eventPort($event.target)" @pointerleave="hover = null"
      @focusin="focus = ($event.target as HTMLElement).matches(':focus-visible') ? eventPort($event.target) : null" @focusout="focus = null">
      <ParticleLinks :stage="stage" :links="links" :active="current" :signals="signals" />
      <button data-port="screen" class="feature-source" type="button" :aria-label="`查看主界面：${source.title}`" @click="emit('source')">
        <img v-if="source.image" :src="source.image" alt="" /><span v-else class="feature-source-empty"><PanelsTopLeft :size="28" aria-hidden="true" />暂无主界面截图</span>
        <span class="feature-label"><span><small>主界面</small><strong>{{ source.title }}</strong></span><ChevronRight :size="16" aria-hidden="true" /></span>
      </button>
      <ul v-if="branches.length" class="feature-cards" aria-label="关联功能">
        <li v-for="(card, index) in branches" :key="index" class="feature-card" :class="{ active: current === `feature-${index}` }" :data-port="`feature-${index}`" :style="operations[index] ? { borderColor: operationColor(operations[index]!) } : undefined">
          <button class="feature-preview" type="button" :aria-label="`查看功能：${card.title}`" @click="emit('preview', index)">
            <span v-if="card.image" class="feature-thumb"><img :src="card.image" alt="" loading="lazy" /></span>
            <span class="feature-label"><span><strong>{{ card.title }}</strong><small v-if="operations[index]" :style="{ color: operationColor(operations[index]!) }">● {{ operationLabel(operations[index]!) }}</small><small v-else>{{ details(card) }}</small></span><ChevronRight :size="16" aria-hidden="true" /></span>
          </button>
          <button v-if="card.code?.length" type="button" class="feature-toggle" :aria-label="`展开代码：${card.title}`" @click="chooseFeature(index)"><GitFork :size="14" aria-hidden="true" />代码关系</button>
        </li>
      </ul>
      <p v-else class="feature-empty">暂无已归类的关联功能。</p>
    </div>
  </div>
</template>
<style scoped>
.feature-map { container-type: inline-size; border: 1px solid #2a3d54; border-radius: 14px; overflow: clip; color: #eaf3ff; background: radial-gradient(ellipse at 44% 45%, #0d1b2c, #080f1b 57%, #070c14); }
.feature-stage { position: relative; isolation: isolate; display: grid; place-items: center; gap: 14px; padding: 20px clamp(13px, 3.5cqw, 44px); }
.feature-stage.has-branches { grid-template-columns: minmax(138px, 1fr) minmax(76px, .7fr) minmax(184px, 1.55fr); gap: 0; }
.feature-source, .feature-card { position: relative; z-index: 1; display: flex; border-radius: 9px; color: inherit; text-align: left; min-width: 0; }
.feature-source { grid-column: 1; flex-direction: column; width: min(100%, 196px); padding: 5px; border: 1px solid #8c6d42; background: #171d24; }
.has-branches .feature-source { justify-self: end; }
.code-toolbar { padding: 12px 18px 0; }
.code-reset { background: #101e30; border: 1px solid #35536e; color: #d5e9fa; border-radius: 6px; padding: 5px 9px; font-size: 12px; }
.feature-source:hover { border-color: #e8bd76; }
.feature-source > img, .feature-source-empty { display: block; width: 100%; aspect-ratio: 9 / 19.6; object-fit: contain; border-radius: 5px; background: #0a121e; }
.feature-source-empty { display: grid; place-content: center; justify-items: center; gap: 8px; color: #b8c9da; font-size: 12px; }
.feature-source .feature-label { padding: 8px 5px 2px; }
.feature-source .feature-label small { color: #e5c89d; }
.feature-cards { grid-column: 3; width: 100%; margin: 0; list-style: none; padding: 0; display: grid; gap: 7px; }
.feature-card { width: 100%; align-items: stretch; border: 1px solid #2b445c; background: #101c2b; overflow: clip; }
.feature-preview, .feature-toggle { background: transparent; border: 0; min-width: 0; display: flex; align-items: center; gap: 8px; text-align: left; }
.feature-preview { flex: 1; min-height: 55px; padding: 5px 9px; }
.feature-toggle { flex: none; border-left: 1px solid #2b445c; padding: 4px 9px; font-size: 11px; color: #bae3ff; }
.feature-toggle svg { flex: none; }
.feature-preview:focus-visible, .feature-toggle:focus-visible { outline-offset: -3px; }
.feature-card.active, .feature-card:hover { background: #142b41; border-color: #679dc2; }
.feature-label { display: flex; justify-content: space-between; align-items: center; gap: 7px; width: 100%; min-width: 0; }
.feature-label > span { min-width: 0; }
.feature-label strong, .feature-label small { display: block; overflow-wrap: anywhere; }
.feature-label strong { font-size: 14px; }
.feature-label small { color: #aec3da; font-size: 11px; }
.feature-label svg { flex: none; color: #9bb7d0; }
.feature-thumb { display: block; flex: 0 0 32px; height: 42px; background: #080f1b; border-radius: 4px; overflow: hidden; }
.feature-thumb img { display: block; width: 100%; height: 100%; object-fit: contain; }
.feature-empty { color: #aec3da; font-size: 12px; }
@container (max-width: 555px) {
  .feature-stage.has-branches { grid-template-columns: minmax(0, 1fr); gap: 29px; }
  .feature-stage .feature-source { justify-self: center; width: min(57%, 178px); }
  .feature-cards { grid-column: 1; padding-left: clamp(32px, 10cqw, 52px); }
}
</style>
