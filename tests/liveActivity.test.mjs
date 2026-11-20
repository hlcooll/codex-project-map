import { test } from 'node:test'
import assert from 'node:assert/strict'
import { emptyActivityState, applyLiveSnapshot, invalidateActivity, expireActivity, visibleActivity } from '../src/liveActivityModel.ts'
const start = Date.now()
const event = (extra={}) => ({id:'e',version:'ff9a',threadId:'one',turnId:'turn',kind:'write',status:'running',at:new Date(start).toISOString(),title:'修改文件',detail:'',paths:['api.go'],targets:[],mapping:'unmapped',...extra})
const snapshot = (events=[event()],extra={}) => ({source:'live',connection:'ready',asOf:new Date(start).toISOString(),sessionCount:1,sessions:[{id:'one',title:'one',cwd:'/project',lastAt:new Date(start).toISOString(),status:'working',association:'workspace'}],events,...extra})
test('history establishes a baseline and opaque version changes pulse once',()=>{
 let state=applyLiveSnapshot(emptyActivityState(),snapshot(),start)
 assert.equal(state.connected,true); assert.equal(state.pulses.length,0)
 state=applyLiveSnapshot(state,snapshot(),start+2000)
 assert.equal(state.pulses.length,0)
 state=applyLiveSnapshot(state,snapshot([event({version:'00ac',status:'completed'})]),start+4000)
 assert.equal(state.pulses.length,1); assert.equal(state.pulses[0].event.status,'completed')
 state=applyLiveSnapshot(state,snapshot([event({version:'00ac',status:'completed'})]),start+6000)
 assert.equal(state.pulses.length,1)
 state=expireActivity(state,start+6500)
 assert.equal(state.pulses.length,0)
 state=applyLiveSnapshot(state,snapshot([event({version:'00ac',status:'completed'})]),start+8000)
 assert.equal(state.pulses.length,0)
})
test('disconnect, source degradation and invalid input stop running states; reconnect does not replay',()=>{
 const initial=applyLiveSnapshot(emptyActivityState(),snapshot(),start)
 for (const state of [invalidateActivity(initial),expireActivity(initial,start+8001),applyLiveSnapshot(initial,snapshot(undefined,{connection:'degraded'}),start+1),applyLiveSnapshot(initial,{bad:true},start+1)]) {
  assert.equal(state.connected,false); assert.equal(state.snapshot.events[0].status,'stale');assert.equal(state.snapshot.sessions[0].status,'stale')
  const next=applyLiveSnapshot(state,snapshot([event({version:'ended',status:'completed'})]),start+9000)
  assert.equal(next.pulses.length,0);assert.equal(next.connected,true)
 }
})
test('new actual events pulse; older history arriving later does not; project reset removes all old state',()=>{
 let state=applyLiveSnapshot(emptyActivityState(),snapshot(),start)
 state=applyLiveSnapshot(state,snapshot([event(),event({id:'new',threadId:'two'}),event({id:'historical',at:new Date(start-86400000).toISOString()})]),start+2000)
 assert.deepEqual(state.pulses.map(p=>p.event.id),['new'])
 assert.equal(visibleActivity(state,'two').length,1);assert.equal(visibleActivity(state,null).length,3)
 state=emptyActivityState();assert.equal(state.seen.size,0);assert.equal(state.snapshot,null);assert.equal(state.pulses.length,0)
})
