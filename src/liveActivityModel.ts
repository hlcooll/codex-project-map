export interface LiveTarget { group: string; screen: string; key?: string; file: string; precision: 'symbol' | 'file' }
export interface LiveEvent {
  id: string; version: string; threadId: string; turnId: string
  kind: 'message' | 'read' | 'write' | 'test' | 'command' | 'tool'
  status: 'running' | 'completed' | 'failed' | 'interrupted' | 'stale' | 'unknown'
  role?: 'user' | 'assistant'; at: string; title: string; detail: string
  paths: string[]; targets: LiveTarget[]; mapping: 'exact' | 'file' | 'unmapped'
}
export interface LiveSession {
  id: string; title: string; cwd: string; lastAt: string
  status: 'working' | 'idle' | 'stale' | 'interrupted'; association: 'workspace' | 'cross-project'
}
export interface LiveSnapshot {
  source: 'live'; connection: 'ready' | 'degraded' | 'unavailable'; asOf: string
  sessionCount: number; sessions: LiveSession[]; events: LiveEvent[]; message?: string
}
export interface ActivityState {
  snapshot: LiveSnapshot | null; connected: boolean; lastReceived: number
  baseline: boolean; seen: Map<string, string>
  pulses: { event: LiveEvent; until: number }[]
}
export function emptyActivityState(): ActivityState {
  return { snapshot: null, connected: false, lastReceived: 0, baseline: false, seen: new Map(), pulses: [] }
}
const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object'
const date = (v: unknown) => typeof v === 'string' && Number.isFinite(Date.parse(v))
export function normalizeLiveSnapshot(value: unknown): LiveSnapshot | null {
  if (!record(value) || value.source !== 'live' || !['ready','degraded','unavailable'].includes(String(value.connection)) || !date(value.asOf) || !Array.isArray(value.events) || !Array.isArray(value.sessions)) return null
  for (const e of value.events) {
    if (!record(e) || !['id','version','threadId','turnId','title','detail'].every(k => typeof e[k] === 'string') || !e.id || !e.version || !date(e.at) || !['message','read','write','test','command','tool'].includes(String(e.kind)) || !['running','completed','failed','interrupted','stale','unknown'].includes(String(e.status)) || !['exact','file','unmapped'].includes(String(e.mapping)) || !Array.isArray(e.paths) || !e.paths.every(p => typeof p === 'string') || !Array.isArray(e.targets)) return null
    if (!e.targets.every(t => record(t) && ['group','screen','file'].every(k => typeof t[k] === 'string') && (t.key === undefined || typeof t.key === 'string') && ['symbol','file'].includes(String(t.precision)))) return null
  }
  if (!value.sessions.every(s => record(s) && ['id','title','cwd'].every(k => typeof s[k] === 'string') && date(s.lastAt) && ['working','idle','stale','interrupted'].includes(String(s.status)) && ['workspace','cross-project'].includes(String(s.association)))) return null
  if (typeof value.sessionCount !== 'number' || (value.message !== undefined && typeof value.message !== 'string')) return null
  return value as unknown as LiveSnapshot
}
export function invalidateActivity(state: ActivityState): ActivityState {
  return { ...state, connected: false, baseline: false, pulses: [], snapshot: state.snapshot ? {
    ...state.snapshot,
    events: state.snapshot.events.map(e => e.status === 'running' ? { ...e, status: 'stale' } : e),
    sessions: state.snapshot.sessions.map(s => s.status === 'working' ? { ...s, status: 'stale' } : s),
  } : null }
}
export function applyLiveSnapshot(state: ActivityState, input: unknown, now = Date.now()): ActivityState {
  const snapshot = normalizeLiveSnapshot(input)
  if (!snapshot) return invalidateActivity(state)
  if (snapshot.connection !== 'ready') return invalidateActivity({ ...state, snapshot, lastReceived: now })
  const seen = new Map(state.seen)
  const pulses = state.pulses.filter(p => p.until > now)
  const previousTime = state.snapshot ? Date.parse(state.snapshot.asOf) : now
  for (const event of snapshot.events) {
    const version = `${event.version}:${event.status}`
    const prior = seen.get(event.id)
    if (state.baseline && prior !== version && (prior !== undefined || Date.parse(event.at) >= previousTime - 10000)) {
      const old = pulses.findIndex(p => p.event.id === event.id)
      if (old >= 0) pulses.splice(old, 1)
      pulses.push({ event, until: now + 2400 })
    }
    seen.delete(event.id); seen.set(event.id, version)
  }
  while (seen.size > 500) seen.delete(seen.keys().next().value!)
  return { snapshot, connected: true, baseline: true, lastReceived: now, seen, pulses: pulses.slice(-60) }
}
export function expireActivity(state: ActivityState, now = Date.now()): ActivityState {
  if (state.connected && now - state.lastReceived >= 8000) return invalidateActivity(state)
  return { ...state, pulses: state.pulses.filter(p => p.until > now) }
}
export function visibleActivity(state: ActivityState, thread: string | null) {
  return (state.snapshot?.events ?? []).filter(e => !thread || e.threadId === thread)
}
