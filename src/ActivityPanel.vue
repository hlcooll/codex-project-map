<script setup lang="ts">
import { computed, ref } from 'vue'
import type { LiveEvent, LiveSnapshot, LiveTarget } from './liveActivity'
import { operationColor, operationLabel } from './activityGraph'
const props = defineProps<{ snapshot: LiveSnapshot | null; connected: boolean; selectedThread: string | null; events: LiveEvent[] }>()
const emit = defineEmits<{ selectThread: [id: string | null]; locate: [target: LiveTarget] }>()
const limit = ref(20)
const sessions = computed(() => props.snapshot?.sessions ?? [])
const sessionStatus = { working: '执行中', idle: '已结束', stale: '待确认', interrupted: '已中断' }
const recent = computed(() => props.events.slice(0, limit.value))
const working = computed(() => props.connected ? sessions.value.filter(s => s.status === 'working').length : 0)
function timestamp(at: string) { return new Date(at).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }) }
function targets(event: LiveEvent) { return event.targets.filter(t => t.key && ((event.mapping === 'exact' && t.precision === 'symbol') || t.group === '项目图谱')) }
</script>
<template>
  <aside class="activity-panel" aria-label="Codex 会话与操作">
    <header class="activity-header">
      <h3>Codex 会话</h3>
      <span class="connection-state" :class="{ ready: connected }" role="status">{{ connected ? (working ? `${working} 个会话执行中` : '已连接 · 当前空闲') : '实时状态暂停' }}</span>
    </header>
    <p class="activity-note">{{ snapshot?.message || (connected ? '点击事件中的卡片入口，查看关联的功能和代码。' : '等待可用的本地记录；历史保留，粒子暂停。') }}</p>
    <label class="session-filter">会话
      <select :value="selectedThread ?? ''" @change="limit = 20; emit('selectThread', ($event.target as HTMLSelectElement).value || null)">
        <option value="">全部关联会话（{{ sessions.length }}）</option>
        <option v-for="s in sessions" :key="s.id" :value="s.id">{{ sessionStatus[s.status] }} · {{ s.association === 'cross-project' ? '跨项目 · ' : '' }}{{ s.title || '未命名会话' }}</option>
      </select>
    </label>
    <div class="activity-legend" aria-label="粒子颜色含义"><span>读取</span><span>修改</span><span>测试</span><span>失败</span></div>
    <p class="activity-scope">粒子表示有依据的源码操作；文件关联不表示其中所有功能都在修改。</p>
    <div class="activity-events" tabindex="0" aria-label="近期操作和对话">
      <p v-if="!events.length" class="activity-empty">{{ snapshot ? '当前筛选没有记录。' : '正在连接本地 Codex 记录…' }}</p>
      <article v-for="event in recent" :key="event.id" class="activity-event" :data-event-id="event.id" :data-status="event.status" :style="{ '--operation-color': event.kind === 'message' ? '#abb9cd' : operationColor(event) }">
        <div class="event-heading"><strong>{{ event.title }}</strong><time :datetime="event.at">{{ timestamp(event.at) }}</time></div>
        <p v-if="!selectedThread" class="event-session">{{ sessions.find(s => s.id === event.threadId)?.title || '关联会话' }}</p>
        <p v-if="event.kind !== 'message'" class="event-status">{{ operationLabel(event) }} <span>· {{ event.mapping === 'exact' ? '精确定位' : event.mapping === 'file' ? '涉及文件' : '未定位到卡片' }}</span></p>
        <p v-else class="event-message">{{ event.detail.slice(0, 180) }}{{ event.detail.length > 180 ? '…' : '' }}</p>
        <div v-if="targets(event).length" class="event-targets">
          <button v-for="target in targets(event)" :key="`${target.group}/${target.screen}/${target.key}/${target.file}`" type="button" :title="target.file" @click="emit('locate', target)">{{target.precision==='symbol'?'定位':'关联'}}：{{ target.screen }}<template v-if="target.group!=='项目图谱'"> · {{ target.key }}</template></button>
        </div>
        <details v-if="event.detail || event.paths.length" class="event-detail">
          <summary>{{ event.kind === 'message' ? '完整消息' : '操作详情与文件' }}</summary>
          <ul v-if="event.paths.length"><li v-for="path in event.paths" :key="path"><code>{{ path === '.' ? '当前项目目录' : path }}</code></li></ul>
          <pre v-if="event.detail">{{ event.detail }}</pre>
        </details>
      </article>
      <button v-if="events.length > limit" class="more-events" type="button" @click="limit += 20">再显示 {{ Math.min(events.length - limit, 20) }} 条</button>
    </div>
    <p v-if="snapshot" class="activity-footer">更新于 {{ timestamp(snapshot.asOf) }} · 近期记录 {{ events.length }} 条</p>
  </aside>
</template>
<style scoped>
.activity-panel { min-width: 0; border: 1px solid #2a3d54; border-radius: 12px; padding: 16px; background: #0b1420; color: #e3edf8; align-self: start; }
.activity-header { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: 8px; }
.activity-header h3 { font-size: 15px; margin: 0; }
.connection-state { font-size: 11px; color: #ddba7c; }
.connection-state.ready { color: #8ad7b9; }
.activity-note, .activity-scope, .activity-footer { color: #9eb1c7; font-size: 11px; line-height: 1.6; margin: 9px 0; }
.session-filter { display: grid; gap: 5px; color: #b9cce1; font-size: 11px; }
.session-filter select { width: 100%; min-width: 0; color: #dceaf9; border: 1px solid #34506b; border-radius: 6px; background: #111f30; padding: 8px; font-size: 12px; }
.activity-legend { display: flex; gap: 16px; margin-top: 13px; font-size: 11px; }
.activity-legend span::before { content: '●'; padding-right: 5px; }
.activity-legend span:nth-child(1) { color: #75d4fa; }.activity-legend span:nth-child(2) { color: #c89cff; }.activity-legend span:nth-child(3) { color: #ffd177; }.activity-legend span:nth-child(4) { color: #ff8484; }
.activity-events { max-height: 530px; overflow: auto; scrollbar-width: thin; padding-right: 5px; }
.activity-event { margin: 9px 0; border-left: 2px solid var(--operation-color); padding: 8px 9px; background: #111e2d; border-radius: 0 6px 6px 0; }
.event-heading { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 4px; font-size: 12px; }
.event-heading time { color: #95aac0; font-size: 10px; }
.event-session { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #93a7bf; font-size: 10px; margin: 5px 0; }
.event-status { font-size: 11px; color: var(--operation-color); margin: 6px 0; }
.event-status span { color: #a4b5c7; }
.event-message { font-size: 12px; line-height: 1.6; overflow-wrap: anywhere; margin: 8px 0; }
.event-targets { display: flex; flex-wrap: wrap; gap: 5px; margin: 7px 0; }
.event-targets button, .more-events { font-size: 11px; padding: 5px 7px; border: 1px solid #345570; border-radius: 4px; color: #c2e6ff; background: #172b40; text-align: left; overflow-wrap: anywhere; }
.event-detail { font-size: 11px; color: #b6c9dd; }
.event-detail summary { cursor: pointer; padding: 4px 0; }
.event-detail pre { margin: 5px 0; white-space: pre-wrap; overflow-wrap: anywhere; font-size: 10px; max-height: 250px; overflow: auto; }
.event-detail ul { padding-left: 15px; overflow-wrap: anywhere; font-size: 10px; }
.activity-empty { font-size: 12px; color: #b0c2d5; padding: 20px 0; }
.activity-footer { margin-bottom: 0; }
</style>
