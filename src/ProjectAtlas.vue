<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import ParticleLinks from './ParticleLinks.vue'
import UIPreviewManager from './UIPreviewManager.vue'
import { projectTopology, topologyLayout } from './projectTopology'
import { planKindLabel,previewKindLabel,previewSourceLabel,type ProjectUI,type PlanDocument,type PlanNode } from './projectPlan'
import type { LiveEvent } from './liveActivity'
import { latestOperation,operationColor,operationLabel,type ActivitySignal } from './activityGraph'
import { zoomScroll, fitZoom } from './atlasViewport'
const props=defineProps<{document:PlanDocument;selectedId:string;focusToken?:number;events:LiveEvent[];pulses:LiveEvent[];connected:boolean;projectId?:string;ui?:ProjectUI;fullscreen?:boolean;rootId?:string}>()
const emit=defineEmits<{select:[id:string];source:[id:string,index:number];edge:[index:number];demo:[path:string];previewsChanged:[];root:[id:string];open:[id:string]}>()
const viewport=ref<HTMLElement|null>(null),stage=ref<HTMLElement|null>(null),active=ref<string|null>(null)
const scope=ref<'all'|'chain'>('all'),moduleId=ref(''),offset=ref(0),zoom=ref(1),group=ref(''),search=ref(''),fitLimited=ref(false)
const rootId=computed(()=>props.fullscreen ? props.rootId??'' : props.selectedId)
const root=computed(()=>props.document.nodes.find(n=>n.id===rootId.value))
const pages=computed(()=>props.document.nodes.filter(n=>n.preview||n.kind==='page'))
const groups=computed(()=>[...new Set(pages.value.map(n=>n.preview?.group??'识别页面'))])
const shownPages=computed(()=>pages.value.filter(n=>!group.value||(n.preview?.group??'识别页面')===group.value))
const modules=computed(()=>props.document.nodes.filter(n=>n.kind==='module'&&n.origin==='scan'))
const range=computed(()=>projectTopology(props.document,rootId.value,scope.value==='chain',moduleId.value,0,Number.MAX_SAFE_INTEGER))
const graph=computed(()=>{
 const nodes=range.value.nodes.slice(offset.value,offset.value+48),shown=new Set(nodes.map(n=>n.id))
 const edges=range.value.edges.filter(e=>shown.has(e.from)&&shown.has(e.to))
 return {nodes,edges,total:range.value.total,totalEdges:range.value.totalEdges,hiddenEdges:range.value.totalEdges-edges.length}
})
const matches=computed(()=>search.value.trim()?range.value.nodes.filter(n=>`${n.title} ${n.description} ${n.category??''}`.toLowerCase().includes(search.value.trim().toLowerCase())):[])
const layout=computed(()=>topologyLayout(graph.value.nodes,graph.value.edges,!!props.fullscreen))
const links=computed(()=>graph.value.edges.map(e=>({from:e.from,to:e.to,label:e.label,scopes:e.scopes,evidence:e.evidence.map(p=>({id:String(e.index),file:p.file,line:p.line}))})))
watch([scope,moduleId],()=>{offset.value=0;search.value='';fitLimited.value=false})
watch(offset,()=>{fitLimited.value=false})
watch([rootId,()=>props.focusToken],async()=>{
 scope.value=rootId.value?'chain':'all';moduleId.value='';offset.value=0;search.value='';fitLimited.value=false
 await nextTick();if(rootId.value)await locateNode(rootId.value);else viewport.value?.scrollTo({left:0,top:0})
},{immediate:true})
watch(()=>props.focusToken,async()=>{if(!props.fullscreen){await nextTick();viewport.value?.scrollIntoView({block:'nearest'})}})
watch(()=>graph.value.total,total=>{if(offset.value>=total)offset.value=0})
function selectPage(node:PlanNode){if(props.fullscreen)emit('root',node.id);else emit('open',node.id)}
function resetScope(){scope.value='all';moduleId.value='';offset.value=0;viewport.value?.scrollTo({left:0,top:0})}
async function locateNode(id:string){
 const index=range.value.nodes.findIndex(n=>n.id===id);if(index<0)return false
 offset.value=Math.floor(index/48)*48;await nextTick()
 const position=layout.value.positions.get(id),el=viewport.value
 if(!position||!el)return false
 el.scrollTo({left:Math.max(0,(position.left+position.width/2)*zoom.value-el.clientWidth/2),top:Math.max(0,(position.top+position.height/2)*zoom.value-el.clientHeight/2)})
 return true
}
defineExpose({locateNode})
async function searchNode(id:string){search.value='';emit('select',id);await locateNode(id)}
async function returnRoot(){scope.value=rootId.value?'chain':'all';moduleId.value='';offset.value=0;await nextTick();if(rootId.value)await locateNode(rootId.value);else viewport.value?.scrollTo({left:0,top:0})}
async function setZoom(value:number,anchor?:{x:number;y:number}){
 const el=viewport.value;if(!el)return
 fitLimited.value=false
 const next=Math.max(.5,Math.min(1.75,value)),point=anchor??{x:el.clientWidth/2,y:el.clientHeight/2}
 const position=zoomScroll({left:el.scrollLeft,top:el.scrollTop},point,zoom.value,next)
 zoom.value=next;await nextTick();el.scrollTo(position)
}
async function fit(){
 const el=viewport.value;if(!el)return
 zoom.value=fitZoom(layout.value, {width:el.clientWidth,height:el.clientHeight});fitLimited.value=layout.value.width*zoom.value>el.clientWidth||layout.value.height*zoom.value>el.clientHeight;await nextTick();el.scrollTo({left:0,top:0})
}
function wheel(event:WheelEvent){if(!event.ctrlKey&&!event.metaKey)return;event.preventDefault();const rect=viewport.value!.getBoundingClientRect();void setZoom(zoom.value*Math.exp(-event.deltaY*.003),{x:event.clientX-rect.left,y:event.clientY-rect.top})}
const dragging=ref(false)
let drag:{id:number;x:number;y:number;left:number;top:number}|null=null
function pointerDown(event:PointerEvent){
 if(event.pointerType!=='mouse'||event.button!==0||(event.target as Element).closest('button,input,select,a,article,dialog'))return
 const el=viewport.value!;drag={id:event.pointerId,x:event.clientX,y:event.clientY,left:el.scrollLeft,top:el.scrollTop};dragging.value=true;el.setPointerCapture(event.pointerId);el.focus({preventScroll:true});event.preventDefault()
}
function pointerMove(event:PointerEvent){if(drag?.id===event.pointerId)viewport.value?.scrollTo({left:drag.left+drag.x-event.clientX,top:drag.top+drag.y-event.clientY})}
function pointerEnd(){drag=null;dragging.value=false}
onBeforeUnmount(pointerEnd)
function operation(node:PlanNode){if(!props.connected)return;return latestOperation(props.events.filter(e=>e.kind!=='message'&&(e.status==='running'||Date.now()-Date.parse(e.at)<60000)&&e.targets.some(t=>t.group==='项目图谱'&&t.key===node.id)))}
const signals=computed(()=>{
 if(!props.connected)return []
 const events=new Map(props.events.filter(e=>e.status==='running').map(e=>[e.id,e]));for(const e of props.pulses)if(['running','completed','failed'].includes(e.status))events.set(e.id,e)
 const result:ActivitySignal[]=[]
 for(const event of events.values())for(const target of event.targets){if(event.mapping!=='exact'||target.precision!=='symbol'||target.group!=='项目图谱')continue
  for(const edge of links.value.filter(e=>e.to===target.key))result.push({id:`${event.id}:${event.version}:${edge.from}:${edge.to}`,from:edge.from,to:edge.to,color:operationColor(event),repeat:event.status==='running'})
 }
 return result.slice(0,30)
})
const modal=ref<HTMLDialogElement|null>(null),previewNode=ref<PlanNode|null>(null),captureIndex=ref(0)
async function preview(node:PlanNode){previewNode.value=node;captureIndex.value=0;await nextTick();modal.value?.showModal()}
const evidenceEdge=computed(()=>graph.value.edges.map(e=>({...e,fromTitle:props.document.nodes.find(n=>n.id===e.from)?.title??e.from,toTitle:props.document.nodes.find(n=>n.id===e.to)?.title??e.to})))
const originLabel=(origin?:string)=>origin==='manifest'?'项目映射':origin==='scan'?'目录结构':'Codex 分析'
</script>
<template>
 <section class="project-atlas" :class="{'atlas-fullscreen':fullscreen}" aria-label="项目全景链路">
  <p v-if="ui && !fullscreen" class="atlas-coverage">已识别 {{ui.total}} 个页面／界面状态 · 已关联预览 {{ui.withPreview}}/{{ui.total}} · 完整界面截图 {{ui.withCapture}}/{{ui.total}}</p><p v-for="warning in (fullscreen ? [] : ui?.warnings)" :key="warning" class="atlas-help">{{warning}}</p>
  <details v-if="pages.length && !fullscreen" class="atlas-pages" open><summary>页面与功能界面 · {{pages.length}} 项</summary>
   <div class="atlas-groups"><button type="button" :aria-pressed="!group" @click="group=''">全部界面</button><button v-for="name in groups" :key="name" type="button" :aria-pressed="group===name" @click="group=name">{{name}}</button></div>
   <div class="atlas-page-grid"><article v-for="node in shownPages" :key="node.id" class="atlas-page" :class="{selected:node.id===selectedId}">
    <button class="page-select" type="button" :aria-label="`查看界面链路：${node.title}`" @click="selectPage(node)"><img v-if="node.preview?.captures[0]" :src="node.preview.captures[0].url" :alt="`${node.title} · ${node.preview.captures[0].label}`" loading="lazy"/><span v-else class="missing-preview">页面已识别<br/>预览待补</span><strong>{{node.title}}</strong><small>{{node.preview?.group??'源码识别'}} · {{node.category||planKindLabel[node.kind]}}</small></button>
    <small v-if="node.preview?.captures[0]" class="capture-kind">{{previewKindLabel[node.preview.captures[0].kind??'reference']}} · {{previewSourceLabel(node.preview.captures[0].source)}}</small>
    <div class="page-actions"><UIPreviewManager v-if="projectId" :project-id="projectId" :node-id="node.id" :title="node.title" :assets="ui?.assets" :captures="node.preview?.captures" @changed="emit('previewsChanged')"/><button v-if="node.preview?.captures.length" type="button" @click="preview(node)">查看界面</button><button v-if="node.preview?.demo" type="button" @click="emit('demo',node.preview.demo)">试玩 Demo</button></div>
   </article></div>
  </details>
  <section class="atlas-diagram"><div v-if="!fullscreen" class="atlas-inline-title"><h3>架构与功能链路 · {{document.nodes.length}} 个节点 / {{document.edges.length}} 条关系</h3><button type="button" @click="emit('open',rootId)">整屏查看链路</button></div>
   <div class="atlas-controls">
    <div class="atlas-scope"><button type="button" :aria-pressed="scope==='all'" @click="resetScope">全局图谱</button><button type="button" :disabled="!root" :aria-pressed="scope==='chain'" @click="returnRoot">当前起点链路</button><select v-if="scope==='all' && modules.length" v-model="moduleId" aria-label="按目录模块查看图谱"><option value="">全部模块</option><option v-for="module in modules" :key="module.id" :value="module.id">{{module.title}}</option></select></div>
    <div class="atlas-search"><input v-model="search" type="search" aria-label="搜索当前范围节点" placeholder="搜索当前范围节点" @keydown.esc.stop="search=''"/><div v-if="search.trim()" class="atlas-search-results"><p v-if="!matches.length">当前范围没有匹配节点</p><button v-for="node in matches.slice(0,20)" :key="node.id" type="button" @click="searchNode(node.id)">{{node.title}} <small>{{planKindLabel[node.kind]}}</small></button><p v-if="matches.length>20">匹配 {{matches.length}} 项，请缩小搜索范围</p></div></div>
    <div class="atlas-zoom"><button type="button" aria-label="缩小图谱" :disabled="zoom<=.5" @click="setZoom(zoom-.1)">−</button><input type="range" min="0.5" max="1.75" step="0.01" :value="zoom" aria-label="图谱缩放比例" @input="setZoom(Number(($event.target as HTMLInputElement).value))"/><output>{{Math.round(zoom*100)}}%</output><button type="button" aria-label="放大图谱" :disabled="zoom>=1.75" @click="setZoom(zoom+.1)">+</button><button type="button" @click="fit">适应当前组</button><button type="button" @click="returnRoot">回到起点</button></div>
   </div>
   <p v-if="fitLimited" class="atlas-help" role="status">已缩至最低 50% 以保留可读性；其余链路可拖动画布查看，或选择节点收窄范围。</p>
   <p v-else class="atlas-help">{{scope==='chain'?`起点：${root?.title??''}`:'全局关系'}} · 拖动空白处平移，Ctrl / ⌘ + 滚轮缩放。粒子表示定位到源码的操作。</p>
   <div ref="viewport" class="atlas-window" :class="{dragging}" tabindex="0" @wheel="wheel" @pointerdown="pointerDown" @pointermove="pointerMove" @pointerup="pointerEnd" @pointercancel="pointerEnd" @lostpointercapture="pointerEnd" aria-label="可横向和纵向滚动的项目链路图">
    <div :style="{width:`${layout.width*zoom}px`,height:`${layout.height*zoom}px`}"><div ref="stage" class="atlas-stage flow-stage" :style="{width:`${layout.width}px`,height:`${layout.height}px`,transform:`scale(${zoom})`}" @pointerleave="active=null">
     <ParticleLinks :stage="stage" :links="links" :active="active" :signals="signals" :revision="zoom" @evidence="emit('edge',Number($event.id))" />
     <article v-for="node in graph.nodes" :key="node.id" :data-port="node.id" class="atlas-node" :class="{selected:node.id===selectedId}" :style="{left:`${layout.positions.get(node.id)!.left}px`,top:`${layout.positions.get(node.id)!.top}px`,width:`${layout.positions.get(node.id)!.width}px`,height:`${layout.positions.get(node.id)!.height}px`,borderColor:operation(node)?operationColor(operation(node)!):undefined}" @pointerenter="active=node.id" @focusin="active=node.id" @focusout="active=null">
      <small>{{node.category||planKindLabel[node.kind]}} · {{originLabel(node.origin)}}</small><button type="button" class="atlas-node-title" @click="emit('select',node.id)">{{node.title}}</button>
      <button v-if="node.preview?.captures[0]" class="atlas-node-preview" type="button" :aria-label="`预览${node.title}`" @click="preview(node)"><img :src="node.preview.captures[0].url" :alt="node.title" loading="lazy"/></button>
      <p v-else :title="node.description">{{node.description}}</p>
      <span v-if="operation(node)" class="atlas-operation" :style="{color:operationColor(operation(node)!)}">{{operationLabel(operation(node)!)}} · {{operation(node)!.targets.some(t=>t.key===node.id&&t.precision==='symbol')?'精确定位':'文件关联'}}</span>
      <div class="atlas-node-actions"><button v-if="node.evidence.some(e=>e.line>0)" type="button" @click="emit('source',node.id,node.evidence.findIndex(e=>e.line>0))">查看源码</button><button type="button" @click="selectPage(node)">{{fullscreen?'以此为起点':'整屏查看链路'}}</button></div>
     </article>
    </div></div>
   </div>
   <div class="atlas-pagination"><span>显示 {{graph.total?offset+1:0}}–{{Math.min(offset+48,graph.total)}} / {{graph.total}} 个节点 · {{graph.edges.length}} / {{graph.totalEdges}} 条关系</span><button v-if="offset>0" type="button" @click="offset=Math.max(0,offset-48)">上一组</button><button v-if="offset+48<graph.total" type="button" @click="offset+=48">下一组</button></div><p v-if="graph.hiddenEdges" class="atlas-help">当前分组未显示 {{graph.hiddenEdges}} 条关系；选择页面或节点后展开完整链路可收窄范围。</p>
   <details v-if="graph.edges.length && !fullscreen" class="atlas-relations"><summary>关系依据（{{graph.edges.length}}）</summary><ul><li v-for="edge in evidenceEdge" :key="edge.index"><span>{{edge.fromTitle}} → {{edge.toTitle}} · {{edge.label}}</span><small>{{originLabel(edge.origin)}} · {{edge.evidence.length&&edge.evidence.every(e=>e.valid)?'源码位置可核对':'依据待补全'}}</small><button v-if="edge.evidence.length" type="button" @click="emit('edge',edge.index)">查看关系依据</button></li></ul></details>
  </section>
  <dialog ref="modal" class="atlas-preview" :aria-label="`界面预览：${previewNode?.title??''}`"><header><strong>{{previewNode?.title}}</strong><button type="button" @click="modal?.close()">关闭预览</button></header><div class="atlas-groups"><button v-for="(capture,index) in previewNode?.preview?.captures" :key="capture.url" type="button" :aria-pressed="index===captureIndex" @click="captureIndex=index">{{capture.label}}</button></div><p v-if="previewNode?.preview?.captures[captureIndex]" class="atlas-help">{{previewKindLabel[previewNode.preview.captures[captureIndex]!.kind??'reference']}} · {{previewSourceLabel(previewNode.preview.captures[captureIndex]!.source)}} · {{previewNode.preview.captures[captureIndex]!.reason}}</p><img v-if="previewNode?.preview?.captures[captureIndex]" :src="previewNode.preview.captures[captureIndex]!.url" :alt="previewNode.title"/></dialog>
 </section>
</template>
<style scoped>
.atlas-coverage{font-size:13px;color:#c7e5d5;margin:0 0 12px;line-height:1.8}.capture-kind{display:block;font-size:10px;color:#a1c4b2;margin-top:6px}.project-atlas{min-width:0}.project-atlas summary{cursor:pointer;font-size:15px;font-weight:600;color:#d1e8dd;margin:4px 0 12px}.atlas-pages,.atlas-diagram{border:1px solid #2c4d40;background:#0d1c16;border-radius:10px;padding:14px;margin-bottom:14px}.atlas-groups,.atlas-controls,.atlas-pagination,.atlas-node-actions,.page-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.project-atlas button,.project-atlas select{font-size:11px;border:1px solid #315146;border-radius:5px;padding:6px 8px;background:#13291f;color:#c1e3d1;cursor:pointer;max-width:100%;overflow-wrap:anywhere}.project-atlas button[aria-pressed=true]{border-color:#87e3c2;background:#234637}.project-atlas button:disabled{opacity:.4;cursor:default}.atlas-page-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:10px;margin-top:12px;max-height:420px;overflow:auto;padding:2px}.atlas-page{border:1px solid #345448;border-radius:8px;min-width:0;background:#14251d;padding:7px}.atlas-page.selected{border-color:#91efcf}.project-atlas .page-select{display:grid;gap:6px;width:100%;border:0;background:none;padding:0;text-align:left}.page-select img,.missing-preview{width:100%;height:165px;object-fit:contain;background:#091712;border-radius:5px}.missing-preview{display:grid;place-content:center;text-align:center;color:#8dac9d;font-size:12px}.page-select strong{font-size:13px}.page-select small{font-size:10px;color:#9bb6a8}.page-actions{margin-top:7px}.page-actions button{padding:3px 5px}.atlas-controls{margin:12px 0}.atlas-help{font-size:11px;line-height:1.7;color:#9bb8aa;margin:8px 0}.atlas-window{max-width:100%;max-height:680px;overflow:auto;background:#091421;border:1px solid #2b465b;border-radius:8px}.atlas-stage{position:relative;transform-origin:top left;isolation:isolate}.atlas-node{position:absolute;box-sizing:border-box;background:#10202d;border:1px solid #37576c;border-radius:8px;padding:12px;z-index:1;display:flex;flex-direction:column;gap:6px}.atlas-node.selected{border-color:#9de9c9;box-shadow:0 0 0 2px #6ca88d33}.atlas-node small{font-size:10px;color:#9abbd3}.project-atlas .atlas-node-title{font-size:14px;font-weight:600;text-align:left;border:0;background:none;padding:0;color:#def0f9;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.atlas-node p{font-size:11px;line-height:1.5;color:#a8bfce;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;margin:0}.atlas-node img{height:110px;object-fit:contain;cursor:pointer;background:#0a1721}.atlas-node-actions{margin-top:auto}.atlas-node-actions button{padding:3px 6px;background:#122a39;border-color:#305267}.atlas-operation{font-size:10px}.atlas-pagination{justify-content:flex-end;margin-top:9px;font-size:10px;color:#abc5b7}.atlas-pagination span{margin-right:auto}.atlas-relations{margin-top:13px}.atlas-relations summary{font-size:12px}.atlas-relations ul{padding:0;list-style:none;max-height:280px;overflow:auto}.atlas-relations li{font-size:11px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:8px 0;border-bottom:1px solid #294538}.atlas-relations small{color:#9ab3a5}.atlas-preview{width:min(900px,92vw);max-height:92vh;overflow:auto;background:#0c1c15;color:#d8ece1;border:1px solid #466a59;border-radius:12px;padding:18px}.atlas-preview::backdrop{background:#000b}.atlas-preview header{display:flex;justify-content:space-between;gap:15px;margin-bottom:12px}.atlas-preview>img{display:block;max-width:100%;max-height:72vh;object-fit:contain;margin:12px auto}.atlas-preview button{font-size:12px}@media(max-width:600px){.atlas-pages,.atlas-diagram{padding:10px}.atlas-page-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.atlas-window{max-height:520px}.atlas-controls select{max-width:180px}.atlas-node-actions button{font-size:11px}}

.atlas-inline-title{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}.atlas-inline-title h3{font-size:15px;margin:0;color:#d1e8dd}.atlas-scope,.atlas-zoom{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.atlas-zoom input{width:85px;accent-color:#83dac5;min-width:40px}.atlas-zoom output{min-width:40px;font-variant-numeric:tabular-nums;font-size:12px}.atlas-controls{position:relative;gap:12px}.atlas-search{position:relative;flex:1;min-width:160px;max-width:280px}.atlas-search input{width:100%;min-width:0;padding:7px 10px;border:1px solid #345368;border-radius:6px;background:#0c1e2c;font-size:13px;color:#daedf5}.atlas-search-results{position:absolute;top:calc(100% + 5px);left:0;right:0;z-index:5;max-height:300px;overflow:auto;background:#122838;border:1px solid #3b6076;border-radius:6px;box-shadow:0 8px 24px #0006}.atlas-search-results button{display:block;width:100%;text-align:left;border:0;border-radius:0;padding:9px 12px;background:transparent}.atlas-search-results button:hover{background:#234637}.atlas-search-results small{color:#9bb8ca}.atlas-search-results p{padding:10px;font-size:12px;color:#b3c6d2}.atlas-window{overscroll-behavior:contain;cursor:grab;scrollbar-width:thin}.atlas-window.dragging{cursor:grabbing;user-select:none}.atlas-window article{cursor:default}.atlas-node .atlas-node-preview{padding:0;border:0;border-radius:4px;background:#091722;min-height:0;width:100%}.atlas-node-preview img{display:block;width:100%;height:110px}.atlas-fullscreen{display:flex;flex-direction:column;min-height:0;min-width:0;height:100%}.atlas-fullscreen .atlas-diagram{display:flex;flex:1;flex-direction:column;min-height:0;min-width:0;margin:0;border:0;border-radius:0;padding:12px 20px;background:#091421;overflow:hidden}.atlas-fullscreen .atlas-controls{margin:0 0 4px;flex-shrink:0}.atlas-fullscreen button,.atlas-fullscreen select{font-size:14px;min-height:32px}.atlas-fullscreen .atlas-search input{font-size:14px;min-height:32px}.atlas-fullscreen .atlas-help{font-size:12px;margin:4px 0 10px;flex-shrink:0}.atlas-fullscreen .atlas-window{flex:1;min-height:0;height:100%;max-height:none;background:#091421;border:1px solid #223d50;border-radius:8px}.atlas-fullscreen .atlas-pagination{font-size:12px;flex-shrink:0}.atlas-fullscreen .atlas-node{padding:12px;gap:6px}.atlas-fullscreen .atlas-node small{font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.atlas-fullscreen .atlas-node-title{font-size:16px;line-height:1.5;min-height:auto}.atlas-fullscreen .atlas-node p{font-size:14px;line-height:1.5}.atlas-fullscreen .atlas-node-preview{min-height:0}.atlas-fullscreen .atlas-node-actions button{font-size:13px}.atlas-fullscreen .atlas-operation{font-size:12px}.atlas-fullscreen .atlas-zoom{margin-left:auto}.atlas-fullscreen .atlas-node-actions{flex-wrap:nowrap}.atlas-fullscreen .atlas-node-actions button{white-space:nowrap}
@media(max-width:600px){.atlas-fullscreen .atlas-diagram{padding:8px}.atlas-fullscreen .atlas-controls{gap:8px}.atlas-fullscreen .atlas-scope{width:100%;gap:6px}.atlas-fullscreen .atlas-search{max-width:none;width:100%;flex-basis:100%}.atlas-fullscreen .atlas-zoom{width:100%;gap:6px;justify-content:space-between}.atlas-fullscreen .atlas-zoom input{flex:1;width:50px}.atlas-fullscreen .atlas-zoom button{font-size:12px;padding:5px 6px}.atlas-fullscreen .atlas-help{font-size:12px;line-height:1.4}.atlas-fullscreen .atlas-pagination{gap:5px}.atlas-fullscreen .atlas-pagination button{padding:3px 6px;font-size:12px}}

.atlas-fullscreen .atlas-node-preview{flex:1;overflow:hidden;display:block}.atlas-fullscreen .atlas-node-preview img{height:100%;max-height:100%;object-fit:contain}
</style>
