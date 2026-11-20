<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import ParticleLinks from './ParticleLinks.vue'
import UIPreviewManager from './UIPreviewManager.vue'
import { projectTopology, topologyLayout } from './projectTopology'
import { planKindLabel,previewKindLabel,previewSourceLabel,type ProjectUI,type PlanDocument,type PlanNode } from './projectPlan'
import type { LiveEvent } from './liveActivity'
import { latestOperation,operationColor,operationLabel,type ActivitySignal } from './activityGraph'
const props=defineProps<{document:PlanDocument;selectedId:string;focusToken?:number;events:LiveEvent[];pulses:LiveEvent[];connected:boolean;projectId?:string;ui?:ProjectUI}>()
const emit=defineEmits<{select:[id:string];source:[id:string,index:number];edge:[index:number];demo:[path:string];previewsChanged:[]}>()
const viewport=ref<HTMLElement|null>(null)
const stage=ref<HTMLElement|null>(null),active=ref<string|null>(null),scope=ref<'all'|'chain'>('all'),moduleId=ref(''),offset=ref(0),zoom=ref(.85),group=ref('')
const pages=computed(()=>props.document.nodes.filter(n=>n.preview||n.kind==='page'))
const groups=computed(()=>[...new Set(pages.value.map(n=>n.preview?.group??'识别页面'))])
const shownPages=computed(()=>pages.value.filter(n=>!group.value||(n.preview?.group??'识别页面')===group.value))
const modules=computed(()=>props.document.nodes.filter(n=>n.kind==='module'&&n.origin==='scan'))
const graph=computed(()=>projectTopology(props.document,props.selectedId,scope.value==='chain',moduleId.value,offset.value))
const layout=computed(()=>topologyLayout(graph.value.nodes,graph.value.edges))
const links=computed(()=>graph.value.edges.map(e=>({from:e.from,to:e.to,label:e.label})))
const selected=computed(()=>props.document.nodes.find(n=>n.id===props.selectedId))
watch([scope,moduleId],()=>{offset.value=0})
watch([()=>props.selectedId,()=>props.focusToken],async()=>{if(!props.selectedId)return;scope.value='chain';moduleId.value='';offset.value=0;await nextTick();viewport.value?.scrollTo({left:0,top:0})},{immediate:true})
watch(()=>props.focusToken,async()=>{await nextTick();viewport.value?.scrollIntoView({block:'nearest'})})
watch(()=>graph.value.total,total=>{if(offset.value>=total)offset.value=0})
function selectPage(node:PlanNode){emit('select',node.id);scope.value='chain';moduleId.value='';offset.value=0}
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
function resetScope(){scope.value='all';moduleId.value='';offset.value=0}
</script>
<template>
 <section class="project-atlas" aria-label="项目全景链路">
  <p v-if="ui" class="atlas-coverage">已识别 {{ui.total}} 个页面／界面状态 · 已关联预览 {{ui.withPreview}}/{{ui.total}} · 完整界面截图 {{ui.withCapture}}/{{ui.total}}</p><p v-for="warning in ui?.warnings" :key="warning" class="atlas-help">{{warning}}</p>
  <details v-if="pages.length" class="atlas-pages" open><summary>页面与功能界面 · {{pages.length}} 项</summary>
   <div class="atlas-groups"><button type="button" :aria-pressed="!group" @click="group=''">全部界面</button><button v-for="name in groups" :key="name" type="button" :aria-pressed="group===name" @click="group=name">{{name}}</button></div>
   <div class="atlas-page-grid"><article v-for="node in shownPages" :key="node.id" class="atlas-page" :class="{selected:node.id===selectedId}">
    <button class="page-select" type="button" :aria-label="`查看界面链路：${node.title}`" @click="selectPage(node)"><img v-if="node.preview?.captures[0]" :src="node.preview.captures[0].url" :alt="`${node.title} · ${node.preview.captures[0].label}`" loading="lazy"/><span v-else class="missing-preview">页面已识别<br/>预览待补</span><strong>{{node.title}}</strong><small>{{node.preview?.group??'源码识别'}} · {{node.category||planKindLabel[node.kind]}}</small></button>
    <small v-if="node.preview?.captures[0]" class="capture-kind">{{previewKindLabel[node.preview.captures[0].kind??'reference']}} · {{previewSourceLabel(node.preview.captures[0].source)}}</small>
    <div class="page-actions"><UIPreviewManager v-if="projectId" :project-id="projectId" :node-id="node.id" :title="node.title" :assets="ui?.assets" :captures="node.preview?.captures" @changed="emit('previewsChanged')"/><button v-if="node.preview?.captures.length" type="button" @click="preview(node)">查看界面</button><button v-if="node.preview?.demo" type="button" @click="emit('demo',node.preview.demo)">试玩 Demo</button></div>
   </article></div>
  </details>
  <details class="atlas-diagram" open><summary>架构与功能链路 · {{document.nodes.length}} 个节点 / {{document.edges.length}} 条关系</summary>
   <div class="atlas-controls"><button type="button" :aria-pressed="scope==='all'" @click="resetScope">全局图谱</button><button type="button" :disabled="!selected" :aria-pressed="scope==='chain'" @click="scope='chain';moduleId=''">所选节点的完整下游链路</button><select v-if="scope==='all'" v-model="moduleId" aria-label="按目录模块查看图谱"><option value="">全部模块</option><option v-for="module in modules" :key="module.id" :value="module.id">{{module.title}}</option></select><label>缩放 <select v-model.number="zoom" aria-label="全景图缩放"><option :value=".55">55%</option><option :value=".85">85%</option><option :value="1">100%</option></select></label></div>
   <p class="atlas-help">{{scope==='chain'?`从“${selected?.title??''}”沿有向关系逐层展开。`:'全局按真实关系排列，可按模块查看。'}} 悬停表示关系选择；状态粒子只播放已精确定位的源码操作。</p>
   <div ref="viewport" class="atlas-window" tabindex="0" aria-label="可横向和纵向滚动的项目链路图">
    <div :style="{width:`${layout.width*zoom}px`,height:`${layout.height*zoom}px`}"><div ref="stage" class="atlas-stage flow-stage" :style="{width:`${layout.width}px`,height:`${layout.height}px`,transform:`scale(${zoom})`}" @pointerleave="active=null">
     <ParticleLinks :stage="stage" :links="links" :active="active" :signals="signals" :revision="zoom" />
     <article v-for="node in graph.nodes" :key="node.id" :data-port="node.id" class="atlas-node" :class="{selected:node.id===selectedId}" :style="{left:`${layout.positions.get(node.id)!.left}px`,top:`${layout.positions.get(node.id)!.top}px`,width:`${layout.positions.get(node.id)!.width}px`,height:`${layout.positions.get(node.id)!.height}px`,borderColor:operation(node)?operationColor(operation(node)!):undefined}" @pointerenter="active=node.id" @focusin="active=node.id" @focusout="active=null">
      <small>{{node.category||planKindLabel[node.kind]}} · {{originLabel(node.origin)}}</small><button type="button" class="atlas-node-title" @click="emit('select',node.id)">{{node.title}}</button>
      <img v-if="node.preview?.captures[0]" :src="node.preview.captures[0].url" :alt="node.title" loading="lazy" @click="preview(node)"/>
      <p v-else :title="node.description">{{node.description}}</p>
      <span v-if="operation(node)" class="atlas-operation" :style="{color:operationColor(operation(node)!)}">{{operationLabel(operation(node)!)}} · {{operation(node)!.targets.some(t=>t.key===node.id&&t.precision==='symbol')?'精确定位':'文件关联'}}</span>
      <div class="atlas-node-actions"><button v-if="node.evidence.some(e=>e.line>0)" type="button" @click="emit('source',node.id,node.evidence.findIndex(e=>e.line>0))">查看源码</button><button type="button" @click="selectPage(node)">展开链路</button></div>
     </article>
    </div></div>
   </div>
   <div class="atlas-pagination"><span>显示 {{graph.total?offset+1:0}}–{{Math.min(offset+48,graph.total)}} / {{graph.total}} 个节点 · {{graph.edges.length}} / {{graph.totalEdges}} 条关系</span><button v-if="offset>0" type="button" @click="offset=Math.max(0,offset-48)">上一组</button><button v-if="offset+48<graph.total" type="button" @click="offset+=48">下一组</button></div><p v-if="graph.hiddenEdges" class="atlas-help">当前分组未显示 {{graph.hiddenEdges}} 条关系；选择页面或节点后展开完整链路可收窄范围。</p>
   <details v-if="graph.edges.length" class="atlas-relations"><summary>关系依据（{{graph.edges.length}}）</summary><ul><li v-for="edge in evidenceEdge" :key="edge.index"><span>{{edge.fromTitle}} → {{edge.toTitle}} · {{edge.label}}</span><small>{{originLabel(edge.origin)}} · {{edge.evidence.length&&edge.evidence.every(e=>e.valid)?'源码位置可核对':'依据待补全'}}</small><button v-if="edge.evidence.length" type="button" @click="emit('edge',edge.index)">查看关系依据</button></li></ul></details>
  </details>
  <dialog ref="modal" class="atlas-preview"><header><strong>{{previewNode?.title}}</strong><button type="button" @click="modal?.close()">关闭预览</button></header><div class="atlas-groups"><button v-for="(capture,index) in previewNode?.preview?.captures" :key="capture.url" type="button" :aria-pressed="index===captureIndex" @click="captureIndex=index">{{capture.label}}</button></div><p v-if="previewNode?.preview?.captures[captureIndex]" class="atlas-help">{{previewKindLabel[previewNode.preview.captures[captureIndex]!.kind??'reference']}} · {{previewSourceLabel(previewNode.preview.captures[captureIndex]!.source)}} · {{previewNode.preview.captures[captureIndex]!.reason}}</p><img v-if="previewNode?.preview?.captures[captureIndex]" :src="previewNode.preview.captures[captureIndex]!.url" :alt="previewNode.title"/></dialog>
 </section>
</template>
<style scoped>
.atlas-coverage{font-size:13px;color:#c7e5d5;margin:0 0 12px;line-height:1.8}.capture-kind{display:block;font-size:10px;color:#a1c4b2;margin-top:6px}.project-atlas{min-width:0}.project-atlas summary{cursor:pointer;font-size:15px;font-weight:600;color:#d1e8dd;margin:4px 0 12px}.atlas-pages,.atlas-diagram{border:1px solid #2c4d40;background:#0d1c16;border-radius:10px;padding:14px;margin-bottom:14px}.atlas-groups,.atlas-controls,.atlas-pagination,.atlas-node-actions,.page-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.project-atlas button,.project-atlas select{font-size:11px;border:1px solid #315146;border-radius:5px;padding:6px 8px;background:#13291f;color:#c1e3d1;cursor:pointer;max-width:100%;overflow-wrap:anywhere}.project-atlas button[aria-pressed=true]{border-color:#87e3c2;background:#234637}.project-atlas button:disabled{opacity:.4;cursor:default}.atlas-page-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:10px;margin-top:12px;max-height:420px;overflow:auto;padding:2px}.atlas-page{border:1px solid #345448;border-radius:8px;min-width:0;background:#14251d;padding:7px}.atlas-page.selected{border-color:#91efcf}.project-atlas .page-select{display:grid;gap:6px;width:100%;border:0;background:none;padding:0;text-align:left}.page-select img,.missing-preview{width:100%;height:165px;object-fit:contain;background:#091712;border-radius:5px}.missing-preview{display:grid;place-content:center;text-align:center;color:#8dac9d;font-size:12px}.page-select strong{font-size:13px}.page-select small{font-size:10px;color:#9bb6a8}.page-actions{margin-top:7px}.page-actions button{padding:3px 5px}.atlas-controls{margin:12px 0}.atlas-controls label{font-size:11px;color:#a0b9ad;display:flex;gap:5px;align-items:center}.atlas-help{font-size:11px;line-height:1.7;color:#9bb8aa;margin:8px 0}.atlas-window{max-width:100%;max-height:680px;overflow:auto;background:#091421;border:1px solid #2b465b;border-radius:8px}.atlas-stage{position:relative;transform-origin:top left;isolation:isolate}.atlas-node{position:absolute;box-sizing:border-box;background:#10202d;border:1px solid #37576c;border-radius:8px;padding:12px;z-index:1;display:flex;flex-direction:column;gap:6px}.atlas-node.selected{border-color:#9de9c9;box-shadow:0 0 0 2px #6ca88d33}.atlas-node small{font-size:10px;color:#9abbd3}.project-atlas .atlas-node-title{font-size:14px;font-weight:600;text-align:left;border:0;background:none;padding:0;color:#def0f9;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.atlas-node p{font-size:11px;line-height:1.5;color:#a8bfce;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;margin:0}.atlas-node img{height:110px;object-fit:contain;cursor:pointer;background:#0a1721}.atlas-node-actions{margin-top:auto}.atlas-node-actions button{padding:3px 6px;background:#122a39;border-color:#305267}.atlas-operation{font-size:10px}.atlas-pagination{justify-content:flex-end;margin-top:9px;font-size:10px;color:#abc5b7}.atlas-pagination span{margin-right:auto}.atlas-relations{margin-top:13px}.atlas-relations summary{font-size:12px}.atlas-relations ul{padding:0;list-style:none;max-height:280px;overflow:auto}.atlas-relations li{font-size:11px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:8px 0;border-bottom:1px solid #294538}.atlas-relations small{color:#9ab3a5}.atlas-preview{width:min(900px,92vw);max-height:92vh;overflow:auto;background:#0c1c15;color:#d8ece1;border:1px solid #466a59;border-radius:12px;padding:18px}.atlas-preview::backdrop{background:#000b}.atlas-preview header{display:flex;justify-content:space-between;gap:15px;margin-bottom:12px}.atlas-preview>img{display:block;max-width:100%;max-height:72vh;object-fit:contain;margin:12px auto}.atlas-preview button{font-size:12px}@media(max-width:600px){.atlas-pages,.atlas-diagram{padding:10px}.atlas-page-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.atlas-window{max-height:520px}.atlas-controls select{max-width:180px}.atlas-node-actions button{font-size:11px}}
</style>
