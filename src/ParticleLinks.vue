<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { linkKey, traceLinks, type GraphLink } from './codeGraph'
import type { CodeNode } from './appCode'
import type { ActivitySignal } from './activityGraph'
interface Point { x: number; y: number }
type Curve = [Point, Point, Point, Point]
interface Edge extends GraphLink { start: Point; end: Point; curves: Curve[]; path: string }
const props = defineProps<{ stage: HTMLElement | null; links: GraphLink[]; active: string | null; revision?: unknown; signals?: ActivitySignal[] }>()
const emit = defineEmits<{ evidence: [node: CodeNode] }>()
const canvas = ref<HTMLCanvasElement | null>(null)
const svg = ref<SVGSVGElement | null>(null)
const graph = shallowRef<{ width: number; height: number; edges: Edge[] } | null>(null)
const traced = computed(() => traceLinks(props.links, props.active))
function edgeFrom(from: string, to: string, start: Point, end: Point, index: number, stacked: boolean, outer: boolean): Edge {
  const dx = end.x - start.x, dy = end.y - start.y
  let curves: Curve[]
  if (outer) {
    const y = 6 + (index % 4) * 5
    const a = { x: start.x + 16, y }, b = { x: end.x - 16, y }
    curves = [
      [start, { x: a.x, y: start.y }, { x: a.x, y: start.y - 12 }, a],
      [a, { x: a.x + (b.x - a.x) / 3, y }, { x: b.x - (b.x - a.x) / 3, y }, b],
      [b, { x: b.x, y: end.y - 12 }, { x: b.x, y: end.y }, end],
    ]
  } else if (stacked) {
    // Leave each card through its bottom border and use the gutter outside the next column.
    const lane = end.x - 21 - (index % 4) * 3
    const corner = { x: lane, y: start.y + Math.min(43, dy * .64) }
    curves = [
      [start, { x: start.x, y: start.y + 10 }, { x: lane, y: start.y + 4 }, corner],
      [corner, { x: lane, y: corner.y + (end.y - corner.y) * .7 }, { x: lane, y: end.y }, end],
    ]
  } else {
    curves = [[start, { x: start.x + dx * .42, y: start.y + dy * .04 }, { x: end.x - dx * .48, y: end.y }, end]]
  }
  const segments = curves.map(([, a, b, c]) => `C ${a.x},${a.y} ${b.x},${b.y} ${c.x},${c.y}`).join(' ')
  return { from, to, start, end, curves, path: `M ${start.x},${start.y} ${segments}` }
}


function measure() {
  if (!props.stage) return
  const stage = props.stage, frame = stage.getBoundingClientRect()
  if (!frame.width || !frame.height) return
  const scale = stage.offsetWidth / frame.width
  const ports = new Map([...stage.querySelectorAll<HTMLElement>('[data-port]')].filter(card => card.offsetWidth > 0).map(card => [card.dataset.port, card.getBoundingClientRect()]))
  const edges: Edge[] = []
  props.links.forEach((link, index) => {
    const source = ports.get(link.from), target = ports.get(link.to)
    if (!source || !target) return
    const flow = stage.classList.contains('flow-stage')
    const stacked = !flow && target.left < source.right + 9 / scale
    const start = {
      x: ((stacked ? source.left + Math.min(37 / scale, source.width * .31) : source.right) - frame.left) * scale,
      y: ((stacked ? source.bottom : source.top + source.height / 2) - frame.top) * scale,
    }
    const end = { x: (target.left - frame.left) * scale, y: (target.top + target.height / 2 - frame.top) * scale }
    const outer = flow && (end.x < start.x || end.x - start.x > 180)
    edges.push({ ...edgeFrom(link.from, link.to, start, end, index, stacked, outer), ...link })
  })
  graph.value = { width: stage.offsetWidth, height: stage.offsetHeight, edges }
}
const starts = computed(() => [...new Map(graph.value?.edges.map(edge => [edge.from, edge])).values()])
function midpoint(edge: Edge) {
  if (edge.curves.length === 3) return { x: edge.start.x + 44, y: edge.start.y - 10 }
  const [p, a, b, q] = edge.curves.at(-1)!
  return { x: (p.x + 3*a.x + 3*b.x + q.x)/8, y: (p.y + 3*a.y + 3*b.y + q.y)/8 }
}
function showEvidence(edge: Edge) {
  const [location, ...rest] = edge.evidence ?? []
  if (location) emit('evidence', { ...location, title: `关系依据 · ${edge.label}`, kind: '源码关系', evidence: rest })
}
function randomFor(key: string) {
  let value = 2166136261
  for (let i = 0; i < key.length; i++) value = Math.imul(value ^ key.charCodeAt(i), 16777619)
  return () => {
    value = (Math.imul(value, 1664525) + 1013904223) >>> 0
    return value / 4294967296
  }
}

// Paint a halo around each real connector. Only redraw on layout or interaction changes.
function drawParticles() {
  const el = canvas.value, layout = graph.value
  if (!el || !layout) return
  const { width, height, edges } = layout
  const ratio = Math.min(window.devicePixelRatio || 1, 2)
  const pixelWidth = Math.round(width * ratio), pixelHeight = Math.round(height * ratio)
  if (el.width !== pixelWidth) el.width = pixelWidth
  if (el.height !== pixelHeight) el.height = pixelHeight
  const ctx = el.getContext('2d')
  if (!ctx) return
  ctx.setTransform(pixelWidth / width, 0, 0, pixelHeight / height, 0, 0)
  ctx.clearRect(0, 0, width, height)
  const active = traced.value
  for (const edge of edges) {
    const random = randomFor(linkKey(edge))
    const phase = random() * Math.PI * 2
    const visibility = active.has(linkKey(edge)) ? 1 : active.size === 0 ? .65 : .31
    for (const [p, a, b, q] of edge.curves) {
      const stacked = edge.curves.length > 1
      const spread = stacked ? 7 : Math.min(30, Math.abs(q.x - p.x) * .14)
      const count = Math.min(460, Math.max(30, Math.round(Math.hypot(q.x - p.x, q.y - p.y) * (stacked ? .5 : 1.5))))
      for (let i = 0; i < count; i++) {
        const t = random(), s = 1 - t
        const x = s ** 3 * p.x + 3 * s * s * t * a.x + 3 * s * t * t * b.x + t ** 3 * q.x
        const y = s ** 3 * p.y + 3 * s * s * t * a.y + 3 * s * t * t * b.y + t ** 3 * q.y
        const dx = s * s * (a.x - p.x) + 2 * s * t * (b.x - a.x) + t * t * (q.x - b.x)
        const dy = s * s * (a.y - p.y) + 2 * s * t * (b.y - a.y) + t * t * (q.y - b.y)
        const norm = Math.hypot(dx, dy) || 1
        const offset = (Math.sin(t * 10 + phase) * .38 + random() + random() - 1) * spread * Math.sin(Math.PI * t)
        const light = random()
        const opacity = (.17 + light ** 2 * .71) * visibility
        ctx.fillStyle = `rgba(${light > .8 ? '172,223,255' : '69,163,249'},${opacity})`
        ctx.beginPath()
        ctx.arc(x - dy * offset / norm, y + dx * offset / norm, light > .945 ? 1.14 : light > .67 ? .71 : .38, 0, Math.PI * 2)
        ctx.fill()
      }
    }
  }
}


const duration = 460
const pulses = shallowRef<{ key: string; path: string; delay: number }[]>([])
let timer: number | undefined
let observer: ResizeObserver | undefined
const reduce = window.matchMedia('(prefers-reduced-motion: reduce)')
function stopPulse() { window.clearTimeout(timer); pulses.value = [] }
async function play() {
  stopPulse()
  if (reduce.matches || !props.active) return
  const active = graph.value?.edges.filter(edge => traced.value.has(linkKey(edge))) ?? []
  // Stage each row of the directed graph in order; parallel dependencies travel together.
  const distance = new Map<string, number>()
  const roots = active.filter(edge => !active.some(parent => parent.to === edge.from)).map(edge => edge.from)
  const queue = [...new Set(roots.length ? roots : [props.active])]
  queue.forEach(id => distance.set(id, 0))
  for (let i = 0; i < queue.length; i++) {
    for (const edge of active.filter(edge => edge.from === queue[i])) {
      if (distance.has(edge.to)) continue
      distance.set(edge.to, distance.get(edge.from)! + 1)
      queue.push(edge.to)
    }
  }
  pulses.value = active.map(edge => ({ key: linkKey(edge), path: edge.path, delay: (distance.get(edge.from) ?? 0) * .36 }))
  await nextTick()
  svg.value?.querySelectorAll<SVGGElement>('.feature-pulse').forEach(group => {
    group.querySelectorAll<SVGAnimationElement>('[data-play]').forEach(anim => anim.beginElementAt(Number(group.dataset.delay)))
  })
  timer = window.setTimeout(stopPulse, Math.max(0, ...pulses.value.map(p => p.delay * 1000)) + duration + 60)
}
// Event particles are independent of hover feedback. Their stable IDs prevent
// a periodic snapshot from restarting a completed event or replaying history.
const livePulses = computed(() => {
  if (!graph.value) return []
  return (props.signals ?? []).flatMap(signal => {
    const edge = graph.value!.edges.find(e => e.from === signal.from && e.to === signal.to)
    return edge ? [{ ...signal, path: edge.path }] : []
  })
})
const liveColors = computed(() => new Map((props.signals ?? []).map(s => [`${s.from}\0${s.to}`, s.color])))
const liveColor = (edge: Edge) => liveColors.value.get(`${edge.from}\0${edge.to}`)
const motionAllowed = ref(!reduce.matches)
const startedSignals = new Set<string>()
async function startSignals() {
  await nextTick()
  const ids = new Set(livePulses.value.map(p => p.id))
  for (const id of startedSignals) if (!ids.has(id)) startedSignals.delete(id)
  if (!motionAllowed.value) return
  svg.value?.querySelectorAll<SVGGElement>('.activity-pulse').forEach(group => {
    const id = group.dataset.event!
    if (startedSignals.has(id)) return
    startedSignals.add(id)
    group.querySelectorAll<SVGAnimationElement>('[data-event-play]').forEach(anim => anim.beginElement())
  })
}
function motionChanged() { motionAllowed.value = !reduce.matches; startedSignals.clear(); void startSignals() }
watch(livePulses, startSignals, { flush: 'post' })
reduce.addEventListener('change', motionChanged)
onBeforeUnmount(() => reduce.removeEventListener('change', motionChanged))
watch([graph, traced], drawParticles, { flush: 'post' })
watch(() => props.active, play, { flush: 'post' })
watch(graph, stopPulse)
watch([() => props.stage, () => props.links, () => props.revision], () => {
  stopPulse(); measure(); observer?.disconnect()
  observer ??= new ResizeObserver(measure)
  if (props.stage) {
    observer.observe(props.stage)
    props.stage.querySelectorAll('[data-port]').forEach(card => observer!.observe(card))
  }
}, { flush: 'post', immediate: true })
reduce.addEventListener('change', stopPulse)
onBeforeUnmount(() => { observer?.disconnect(); reduce.removeEventListener('change', stopPulse); stopPulse() })
</script>
<template>
  <canvas ref="canvas" class="particle-canvas" aria-hidden="true"></canvas>
  <svg v-if="graph" ref="svg" class="feature-edges" :class="{ 'has-active': traced.size }" aria-hidden="true" focusable="false">
    <defs>
      <linearGradient id="app-feature-light"><stop stop-color="#45bbff" stop-opacity="0" /><stop offset=".74" stop-color="#5cdcff" stop-opacity=".82" /><stop offset="1" stop-color="#f0ffff" /></linearGradient>
      <marker id="app-code-direction" viewBox="0 0 6 6" refX="9" refY="3" markerWidth="6" markerHeight="6" markerUnits="userSpaceOnUse" orient="auto"><path d="M 0 0 L 6 3 L 0 6" fill="none" stroke="#8cc9e8" /></marker>
    </defs>
    <path v-for="edge in graph.edges" :key="linkKey(edge)" class="feature-edge" :class="{ active: traced.has(linkKey(edge)) }" :d="edge.path" :data-from="edge.from" :data-to="edge.to" :style="liveColor(edge) ? { stroke: liveColor(edge), opacity: 1, strokeWidth: 1.8 } : undefined" :marker-end="edge.label ? 'url(#app-code-direction)' : undefined" />
    <circle v-for="edge in starts" :key="edge.from" :cx="edge.start.x" :cy="edge.start.y" r="3.3" class="feature-source-port" />
    <circle v-for="edge in graph.edges" :key="linkKey(edge)" :cx="edge.end.x" :cy="edge.end.y" r="3" class="feature-port" />
    <g v-for="pulse in pulses" :key="pulse.key" class="feature-pulse" opacity="0" :data-delay="pulse.delay">
      <animateMotion :path="pulse.path" rotate="auto" begin="indefinite" :dur="`${duration}ms`" data-play="" />
      <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;.09;.84;1" begin="indefinite" :dur="`${duration}ms`" data-play="" />
      <path d="M -36 0 Q -10 -1.5 0 -2.8 Q 9 0 0 2.8 Q -10 1.5 -36 0 Z" fill="url(#app-feature-light)" /><circle r="1.8" fill="#f3ffff" />
    </g>
    <template v-if="motionAllowed">
      <g v-for="signal in livePulses" :key="signal.id" class="activity-pulse" opacity="0" :data-event="signal.id" :data-to="signal.to" :style="{ color: signal.color }">
        <animateMotion :path="signal.path" rotate="auto" begin="indefinite" dur="1.4s" :repeatCount="signal.repeat ? 'indefinite' : '1'" data-event-play="" />
        <animate attributeName="opacity" values="0;1;1;0;0" keyTimes="0;.08;.55;.75;1" begin="indefinite" dur="1.4s" :repeatCount="signal.repeat ? 'indefinite' : '1'" data-event-play="" />
        <path d="M -22 0 L 0 -2 L 4 0 L 0 2 Z" fill="currentColor" opacity=".75" /><circle r="2.3" fill="currentColor" /><circle cx="-10" r="1.2" fill="currentColor" />
      </g>
    </template>
  </svg>
  <template v-for="edge in graph?.edges" :key="linkKey(edge)">
    <component :is="edge.evidence?.length ? 'button' : 'span'" v-if="edge.label" class="relation-label" :class="{ active: traced.has(linkKey(edge)) }"
      :style="{ left: `${midpoint(edge).x}px`, top: `${midpoint(edge).y - 13}px` }" :type="edge.evidence?.length ? 'button' : undefined"
      :title="edge.label" :aria-label="edge.evidence?.length ? `查看关系依据：${edge.label}` : undefined" @click="showEvidence(edge)">{{ edge.label }}</component>
  </template>
</template>
<style scoped>
.particle-canvas, .feature-edges { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; }
.feature-edges { z-index: 2; overflow: visible; }
.feature-edge { fill: none; stroke: #589ad1; stroke-width: 1; opacity: .6; }
.has-active .feature-edge { opacity: .2; }
.has-active .feature-edge.active { stroke: #99e9ff; stroke-width: 1.8; opacity: 1; filter: drop-shadow(0 0 4px #67cfff); }
.feature-source-port { fill: #ffedc7; filter: drop-shadow(0 0 5px #ffd07c); }
.feature-port { fill: #82bcf0; filter: drop-shadow(0 0 4px #5b9fd7); }
.activity-pulse { filter: drop-shadow(0 0 5px currentColor); }
.feature-pulse { filter: drop-shadow(0 0 5px #49dfff) drop-shadow(0 0 7px #3188ff); }
.relation-label { position: absolute; z-index: 3; transform: translate(-50%, -50%); max-width: 98px; border: 1px solid transparent; border-radius: 4px; padding: 2px 4px; background: #0a1524; color: #92b6d5; font-size: 10px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
button.relation-label:hover, .relation-label.active { color: #ccf7ff; border-color: #355e78; }
@media (prefers-reduced-motion: reduce) { .feature-pulse, .activity-pulse { display: none; } }
</style>
