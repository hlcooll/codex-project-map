<script setup lang="ts">
import { computed, ref } from 'vue'
import ParticleLinks from './ParticleLinks.vue'
import type { PlanDocument, PlanNode } from './projectPlan'
import { planKindLabel } from './projectPlan'
import type { LiveEvent } from './liveActivity'
import { latestOperation, operationColor, operationLabel, type ActivitySignal } from './activityGraph'
const props=defineProps<{document:PlanDocument;selected:PlanNode|null;projectName:string;events:LiveEvent[];freshEvents:LiveEvent[];connected:boolean}>()
const emit=defineEmits<{select:[id:string];source:[node:string,index:number]}>()
const stage=ref<HTMLElement|null>(null), active=ref<string|null>(null), expanded=ref(false)
const relevant=(file:string)=>!props.selected||props.selected.evidence.some(e=>e.file===file||(e.line===0&&file.startsWith(`${e.file}/`)))
const files=computed(()=>{
 const paths=new Set<string>()
 for(const e of props.selected?.evidence??[]) if(e.line>0)paths.add(e.file)
 for(const event of props.events)for(const file of event.paths)if(file!=='.'&&relevant(file))paths.add(file)
 return [...paths]
})
const relations=computed(()=>props.selected?props.document.edges.filter(e=>e.from===props.selected!.id||e.to===props.selected!.id).map(edge=>({edge,node:props.document.nodes.find(n=>n.id===(edge.from===props.selected!.id?edge.to:edge.from))})).filter(x=>x.node):[])
const items=computed(()=>{
 const fileItems=files.value.map(file=>({port:`file:${file}`,file,node:undefined as PlanNode|undefined,label:'关联源码',verified:true}))
 const nodeItems=[...new Map(relations.value.map(({node})=>[node!.id,node!])).values()].map(node=>{
  const edges=relations.value.filter(r=>r.node!.id===node.id).map(r=>r.edge)
  return {port:`node:${node.id}`,file:'',node,label:[...new Set(edges.map(e=>e.label))].join(' · '),verified:edges.every(e=>e.evidence.length>0&&e.evidence.every(p=>p.valid))}
 })
 return [...fileItems,...nodeItems].slice(0,expanded.value?60:10)
})
const links=computed(()=>items.value.flatMap(item=>{
 if(item.file)return [{from:'project-source',to:item.port}]
 const edges=relations.value.filter(r=>r.node!.id===item.node!.id).map(r=>r.edge)
 const directions=[...new Set(edges.map(e=>e.from===props.selected?.id))]
 return directions.map(outgoing=>({from:outgoing?'project-source':item.port,to:outgoing?item.port:'project-source'}))
}))
function operation(file:string){return props.connected?latestOperation(props.events.filter(e=>e.paths.includes(file)&&(e.status==='running'||Date.now()-Date.parse(e.at)<60000))):undefined}
const signals=computed(()=>{
 if(!props.connected)return []
 const events=new Map(props.events.filter(e=>e.status==='running').map(e=>[e.id,e]))
 for(const event of props.freshEvents)if(['running','completed','failed'].includes(event.status))events.set(event.id,event)
 return [...events.values()].flatMap(event=>items.value.filter(item=>item.file&&event.paths.includes(item.file)).map((item):ActivitySignal=>({id:`${event.id}:${event.version}:${item.port}`,from:'project-source',to:item.port,color:operationColor(event),repeat:event.status==='running'}))).slice(0,30)
})
function source(file:string){for(const n of props.document.nodes){const i=n.evidence.findIndex(e=>e.file===file&&e.line>0);if(i>=0){emit('source',n.id,i);return}}}
const navigable=(file:string)=>props.document.nodes.some(n=>n.evidence.some(e=>e.file===file&&e.line>0))
</script>
<template>
 <div class="project-graph">
  <p class="graph-note">{{ selected ? '功能关联与源码操作' : '项目近期文件操作' }} · 粒子表示文件操作，关联线不表示请求正在运行。</p>
  <div ref="stage" class="generic-stage" @pointerleave="active=null">
   <ParticleLinks :stage="stage" :links="links" :active="active" :signals="signals" />
   <article data-port="project-source" class="generic-source" @pointerenter="active='project-source'">
    <small>{{ selected?planKindLabel[selected.kind]:'项目' }}</small><strong>{{ selected?.title??projectName }}</strong>
    <p>{{ selected?.description??'汇集本项目会话真正涉及的文件。选择上方卡片可收窄范围。' }}</p>
   </article>
   <div class="generic-targets">
    <article v-for="item in items" :key="item.port" :data-port="item.port" class="generic-target" :style="item.file&&operation(item.file)?{borderColor:operationColor(operation(item.file)!)}:undefined" @pointerenter="active=item.port">
     <template v-if="item.file">
      <small>文件操作</small><code>{{ item.file }}</code>
      <span v-if="operation(item.file)" class="file-operation" :data-status="operation(item.file)!.status" :style="{color:operationColor(operation(item.file)!)}">● {{ operationLabel(operation(item.file)!) }}</span>
      <button v-if="navigable(item.file)" type="button" @click="source(item.file)">查看源码</button>
     </template>
     <template v-else-if="item.node">
      <small>{{ item.label }} · {{ item.verified?'有源码定位依据':'关系待确认' }}</small>
      <button type="button" @click="emit('select',item.node.id)">{{ item.node.title }} →</button>
     </template>
    </article>
    <p v-if="!items.length" class="no-activity">暂无关联文件操作，读取或修改项目后会显示在这里。</p>
   </div>
  </div>
  <button v-if="files.length+relations.length>10" class="graph-more" type="button" @click="expanded=!expanded">{{expanded?'收起':'展开更多关联'}}</button>
 </div>
</template>
<style scoped>
.project-graph{border:1px solid #2a3d54;border-radius:12px;background:#091321;color:#deebfa;overflow:clip}.graph-note{font-size:11px;color:#9fb8d0;padding:13px 15px 0}.generic-stage{position:relative;isolation:isolate;display:grid;grid-template-columns:minmax(130px,1fr) minmax(55px,.45fr) minmax(170px,1.4fr);padding:24px 18px;align-items:center;min-height:240px}.generic-source,.generic-target{position:relative;z-index:1;padding:13px;border:1px solid #345470;border-radius:8px;background:#101e2d;min-width:0}.generic-source{border-color:#8c6d42}.generic-source strong,.generic-target code{display:block;font-size:13px;overflow-wrap:anywhere;white-space:normal;margin:6px 0}.generic-source p{font-size:11px;color:#a9bed1;line-height:1.7}.generic-source small,.generic-target small{color:#a8c8e1;font-size:10px}.generic-targets{grid-column:3;display:grid;gap:10px;min-width:0}.generic-target button,.graph-more{display:block;background:none;border:0;color:#91dcff;padding:4px 0;font-size:11px;text-align:left;overflow-wrap:anywhere}.file-operation{display:block;font-size:11px;margin:6px 0}.no-activity{font-size:12px;color:#95abc3}.graph-more{padding:8px 16px 14px}@media(max-width:700px){.generic-stage{grid-template-columns:minmax(0,1fr);gap:30px}.generic-source{max-width:250px}.generic-targets{grid-column:1;padding-left:35px}}
</style>
