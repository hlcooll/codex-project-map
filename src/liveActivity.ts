import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { applyLiveSnapshot, emptyActivityState, expireActivity, invalidateActivity, visibleActivity } from './liveActivityModel'
export type { LiveEvent, LiveTarget, LiveSnapshot, LiveSession } from './liveActivityModel'

export function useProjectActivity(projectId: () => string) {
  const state = shallowRef(emptyActivityState())
  const selectedThread = ref<string | null>(null)
  let source: EventSource | undefined
  let timer: number | undefined
  const stop = () => { source?.close(); source = undefined; window.clearInterval(timer) }
  watch(projectId, id => {
    stop(); state.value = emptyActivityState(); selectedThread.value = null
    if (!id) return
    const stream = new EventSource(`/api/events?project=${encodeURIComponent(id)}`)
    source = stream
    stream.onmessage = message => {
      if (source !== stream) return
      try { state.value = applyLiveSnapshot(state.value, JSON.parse(message.data)) }
      catch { state.value = invalidateActivity(state.value) }
    }
    stream.onerror = () => { if (source === stream) state.value = invalidateActivity(state.value) }
    timer = window.setInterval(() => { state.value = expireActivity(state.value) }, 1000)
  }, { immediate: true })
  onBeforeUnmount(stop)
  return {
    snapshot: computed(() => state.value.snapshot), connected: computed(() => state.value.connected),
    lastReceived: computed(() => state.value.lastReceived), selectedThread,
    visibleEvents: computed(() => visibleActivity(state.value, selectedThread.value)),
    pulses: computed(() => state.value.pulses.map(p => p.event).filter(e => !selectedThread.value || e.threadId === selectedThread.value)),
  }
}
