import type { LiveEvent } from './liveActivity'
export interface ActivitySignal { id: string; from: string; to: string; color: string; repeat: boolean }
export function operationColor(event: LiveEvent) {
 if (event.status === 'failed') return '#ff8484'
 if (event.status === 'interrupted' || event.status === 'stale' || event.status === 'unknown') return '#9caebc'
 if (event.kind === 'write') return '#c89cff'
 if (event.kind === 'test') return '#ffd177'
 return '#75d4fa'
}
export function operationLabel(event: LiveEvent) {
 const running = event.status === 'running'
 if (event.status === 'failed') return event.kind === 'test' ? '测试失败' : '操作失败'
 if (event.status === 'stale') return '状态待确认'
 if (event.status === 'interrupted') return '操作已中断'
 if (event.status === 'unknown') return '结果待确认'
 if (event.kind === 'write') return running ? '正在修改' : '修改已记录'
 if (event.kind === 'test') return running ? '正在测试' : '测试已结束'
 if (event.kind === 'read') return running ? '正在读取' : '读取已结束'
 return running ? '正在执行' : '执行已结束'
}
export function isExactTarget(event: LiveEvent, group: string, screen: string, key?: string) {
 return event.mapping === 'exact' && event.targets.some(t => t.precision === 'symbol' && t.group === group && t.screen === screen && (!key || t.key === key))
}
export function latestOperation(events: readonly LiveEvent[]) {
 return [...events].sort((a,b) => Number(b.status === 'running')-Number(a.status === 'running') || Date.parse(b.at)-Date.parse(a.at))[0]
}
export function activitySignals(events: readonly LiveEvent[], fresh: readonly LiveEvent[], edges: {from:string;to:string}[], group: string, screen: string, connected: boolean): ActivitySignal[] {
 if (!connected) return []
 const candidates = new Map(events.filter(e => e.status === 'running').map(e => [e.id, e]))
 for (const event of fresh) if (event.kind !== 'message' && ['running','completed','failed'].includes(event.status)) candidates.set(event.id,event)
 const result: ActivitySignal[] = []
 for (const event of candidates.values()) {
  for (const target of event.targets) {
   if (target.precision !== 'symbol' || event.mapping !== 'exact' || target.group !== group || target.screen !== screen || !target.key) continue
   for (const edge of edges.filter(e => e.to === `code:${target.key}`)) result.push({ id: `${event.id}:${event.version}:${edge.from}:${edge.to}`, ...edge, color: operationColor(event), repeat: event.status === 'running' })
  }
 }
 return result.slice(0,24)
}
