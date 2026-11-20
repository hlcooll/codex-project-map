<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import ActivityPanel from './ActivityPanel.vue'
import PlanningPanel from './PlanningPanel.vue'
import ProjectGraph from './ProjectGraph.vue'
import ProjectAtlas from './ProjectAtlas.vue'
import AnalysisCoverage from './AnalysisCoverage.vue'
import AnalysisContext from './AnalysisContext.vue'
import { usePlanSource } from './usePlanSource'
import type { LiveEvent,LiveSnapshot,LiveTarget } from './liveActivity'
import {planKindLabel,type PlanState,type StartProjectRun} from './projectPlan'
const props=defineProps<{project:{id:string;name:string;root:string};snapshot:LiveSnapshot|null;connected:boolean;selectedThread:string|null;events:LiveEvent[];pulses:LiveEvent[];suspended?:boolean}>()
const emit=defineEmits<{selectThread:[id:string|null];appTarget:[target:LiveTarget];demo:[path:string];chain:[id:string]}>()
const locateRequest=ref(0)
const state=ref<PlanState|null>(null),error=ref(''),loadError=ref(''),busy=ref(false),selectedId=ref(''),search=ref(''),showPlanning=ref(false),follow=ref(false)
const selected=computed(()=>state.value?.document.nodes.find(n=>n.id===selectedId.value)??null)
const filtered=computed(()=>state.value?.document.nodes.filter(n=>`${n.title} ${n.description} ${planKindLabel[n.kind]}`.toLowerCase().includes(search.value.toLowerCase()))??[])
const activeRun=computed(()=>state.value?.runs.find(r=>r.status==='starting'||r.status==='running'))
const versions=ref<{revision:number;createdAt:string}[]>([]),showVersions=ref(false)
const {snippet,snippetError,sourceLoading,source,closeSource}=usePlanSource(()=>props.project.id,()=>state.value?.document)
let loadRequest=0,scanAttempted=false
let controller:AbortController|undefined,timer:number|undefined,generation=0
async function load(){
 const id=props.project.id,gen=generation,request=++loadRequest
 try{const response=await fetch(`/api/projects/${encodeURIComponent(id)}/workbench`,{signal:controller?.signal});if(!response.ok)throw new Error('读取项目档案失败。');const data=await response.json() as PlanState;if(gen===generation&&request===loadRequest){loadError.value='';state.value=data;if(!data.analysis&&!scanAttempted){scanAttempted=true;void scanInventory()}if(selectedId.value&&!data.document.nodes.some(n=>n.id===selectedId.value))selectedId.value=''}}catch(cause){if(gen===generation&&request===loadRequest&&!controller?.signal.aborted)loadError.value=cause instanceof Error?cause.message:'连接失败'}
}
watch(()=>props.project.id,()=>{
 generation++;scanAttempted=false;controller?.abort();window.clearTimeout(timer);controller=new AbortController();state.value=null;error.value='';selectedId.value='';search.value='';showPlanning.value=false;busy.value=false;closeSource();loadError.value='';follow.value=false;showVersions.value=false;versions.value=[]
 const gen=generation
 const poll=async()=>{if(!props.suspended||!state.value)await load();if(gen===generation&&!controller?.signal.aborted)timer=window.setTimeout(poll,2500)};void poll()
},{immediate:true})
watch(selectedId,closeSource)
onBeforeUnmount(()=>{generation++;controller?.abort();window.clearTimeout(timer)})
async function scanInventory(){
 if(busy.value||activeRun.value)return;busy.value=true;error.value='';const gen=generation
 try{const response=await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/analysis/scan`,{method:'POST',signal:controller?.signal});if(!response.ok)throw new Error((await response.text()).trim());if(gen===generation)await load()}
 catch(cause){if(gen===generation&&!controller?.signal.aborted)error.value=cause instanceof Error?cause.message:'扫描失败'}finally{if(gen===generation)busy.value=false}
}
async function start(input:StartProjectRun){
 if(busy.value)return;busy.value=true;error.value='';const gen=generation;showPlanning.value=true
 try{const response=await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/runs`,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...input,requestId:crypto.randomUUID()}),signal:controller?.signal});if(!response.ok)throw new Error((await response.text()).trim());if(gen===generation)await load()}
 catch(cause){if(gen===generation&&!controller?.signal.aborted)error.value=cause instanceof Error?cause.message:'发起任务失败。'}finally{if(gen===generation)busy.value=false}
}
async function interrupt(id:string){
 if(busy.value)return;busy.value=true;error.value='';const gen=generation
 try{const response=await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/runs/${encodeURIComponent(id)}/interrupt`,{method:'POST',signal:controller?.signal});if(!response.ok)throw new Error((await response.text()).trim());if(gen===generation)await load()}
 catch(cause){if(gen===generation&&!controller?.signal.aborted)error.value=cause instanceof Error?cause.message:'停止请求失败。'}finally{if(gen===generation)busy.value=false}
}
async function loadVersions(){
 const gen=generation;showVersions.value=!showVersions.value;if(!showVersions.value)return
 try{const response=await fetch(`/api/projects/${encodeURIComponent(props.project.id)}/plan-versions`,{signal:controller?.signal});if(!response.ok)throw new Error('版本记录读取失败。');const data=await response.json();if(gen===generation)versions.value=data}catch(cause){if(gen===generation&&!controller?.signal.aborted)error.value=cause instanceof Error?cause.message:'版本读取失败'}
}
function locate(target:LiveTarget){if(target.group==='项目图谱'){selectedId.value=target.key??'';locateRequest.value++}else emit('appTarget',target)}
let followed=''
watch(()=>props.pulses,events=>{if(!follow.value||props.suspended)return;const event=[...events].reverse().find(e=>e.targets.some(t=>t.group==='项目图谱'));if(!event||followed===`${event.id}:${event.version}`)return;followed=`${event.id}:${event.version}`;const target=event.targets.find(t=>t.group==='项目图谱');if(target)locate(target)})
const nodeState={existing:'已有实现 · 未代表验收',planned:'计划新增',unknown:'待确认'}
</script>
<template>
 <div class="project-scan universal-workspace">
  <header class="workbench-header"><div><h2>{{project.name}}</h2><p class="scan-root">{{project.root}}</p></div><div class="workbench-actions">
   <button class="dialog-button primary" type="button" :disabled="!state?.connection.available||busy||!!activeRun" @click="start({role:'plan',analysis:'full',prompt:''})">{{state?.plannerThreadId.trim()?'继续完整分析':'完整分析项目'}}</button>
   <button class="dialog-button" type="button" @click="showPlanning=!showPlanning">{{showPlanning?'收起规划与任务':'规划与任务'}}</button>
   <button v-if="state?.revision" class="dialog-button" type="button" @click="loadVersions">档案版本</button>
  </div></header>
  <p v-if="error||loadError" class="scan-feedback form-error" role="alert">{{error||loadError}}</p>
  <p v-if="!state" class="scan-feedback">正在读取项目档案…</p>
  <template v-else>
   <div class="document-meta"><span>{{state.document.kind}}</span><span>{{state.source==='codex'?`Codex 档案 · 第 ${state.revision} 版`:'目录扫描 · 尚未由 Codex 规划'}}</span><span v-for="stack in state.document.stack" :key="stack">{{stack}}</span></div>
   <p class="document-summary">{{state.document.summary}}</p>
   <div v-if="showVersions" class="version-downloads"><a v-for="version in versions" :key="version.revision" :href="`/api/projects/${encodeURIComponent(project.id)}/plan-versions?revision=${version.revision}`">下载第 {{version.revision}} 版 · {{new Date(version.createdAt).toLocaleString('zh-CN')}}</a></div>
   <AnalysisContext :state="state" />
   <AnalysisCoverage :analysis="state.analysis" :busy="busy" :running="!!activeRun" :can-analyze="state.connection.available" @scan="scanInventory" @analyze="start({role:'plan',analysis:'module',moduleId:$event,prompt:''})" />
   <div v-if="activeRun" class="active-project-run" role="status"><span>{{activeRun.title}} · {{activeRun.stage||(activeRun.status==='starting'?'正在连接':'执行中')}}</span><button type="button" class="dialog-button" :disabled="busy" @click="interrupt(activeRun.id)">停止任务</button></div>
   <PlanningPanel v-if="showPlanning" :key="project.id" :state="state" :selected-node="selected" :busy="busy" :error="error" @start="start" @interrupt="interrupt" />
   <div class="universal-columns">
    <section class="universal-map" aria-label="通用项目图谱">
     <ProjectAtlas @open="emit('chain',$event)" :project-id="project.id" :ui="state.ui" @previews-changed="load" :document="state.document" :selected-id="selectedId" :focus-token="locateRequest" :events="events" :pulses="pulses" :connected="connected" @select="selectedId=$event" @source="source" @edge="source('',0,$event)" @demo="emit('demo',$event)" />
     <details class="all-node-details"><summary>全部架构与功能卡片（{{state.document.nodes.length}}）</summary>
     <div class="node-tools"><h3>项目架构与功能</h3><input v-model="search" type="search" aria-label="筛选项目卡片" placeholder="筛选模块、功能、文件" /></div>
     <div class="plan-node-grid">
      <button v-for="node in filtered" :key="node.id" type="button" class="plan-node" :aria-pressed="selectedId===node.id" @click="selectedId=node.id;snippet=null">
       <small>{{planKindLabel[node.kind]}} · {{nodeState[node.state]}}</small><strong>{{node.title}}</strong><p>{{node.description}}</p>
       <span>{{node.evidence.length?`${node.evidence.filter(e=>e.valid).length}/${node.evidence.length} 处源码位置可用`:'待补源码依据'}}</span>
      </button>
     </div>
     <p v-if="!filtered.length" class="scan-feedback">{{state.document.nodes.length?'没有匹配的卡片。':'项目暂无可识别的源码文件，可先补充需求并创建规划。'}}</p>
     </details>
     <div class="selection-tools"><button class="dialog-button" type="button" :aria-pressed="!selected" @click="selectedId='';snippet=null">全部近期文件</button><button v-if="selected" class="dialog-button" type="button" @click="showPlanning=true">为“{{selected.title}}”发起任务</button><label><input v-model="follow" type="checkbox" />跟随新操作</label></div>
     <details class="file-operation-details"><summary>查看实际文件操作与直接关联</summary><ProjectGraph :document="state.document" :selected="selected" :project-name="project.name" :events="events" :fresh-events="pulses" :connected="connected" @select="selectedId=$event" @source="source" /></details>
     <section v-if="selected" class="node-evidence"><h4>{{selected.title}} · 源码与验收</h4>
      <ul><li v-for="(e,index) in selected.evidence" :key="`${e.file}:${index}`"><button v-if="e.line>0" type="button" @click="source(selected!.id,index)">{{e.file}}:{{e.line}}</button><code v-else>{{e.file}}</code><span>{{e.valid?'位置已核对':e.reason||'待确认'}}</span></li></ul>
      <p v-if="!selected.evidence.length">尚无源码依据。</p><ul v-if="selected.acceptance.length"><li v-for="criterion in selected.acceptance" :key="criterion">验收：{{criterion}}</li></ul>
     </section>
     <section v-if="snippet||snippetError||sourceLoading" class="universal-source"><button class="dialog-button" type="button" @click="closeSource()">关闭源码</button><p v-if="sourceLoading">正在读取…</p><p v-else-if="snippetError">{{snippetError}}</p><template v-else-if="snippet"><h4>{{snippet.file}}</h4><pre tabindex="0"><code v-for="(line,i) in snippet.lines" :key="i">{{snippet.start+i}}  {{line}}{{'\n'}}</code></pre></template></section>
     <details v-if="state.document.questions.length" class="plan-questions"><summary>待确认事项（{{state.document.questions.length}}）</summary><ul><li v-for="question in state.document.questions" :key="question">{{question}}</li></ul></details>
    </section>
    <ActivityPanel :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :events="events" @select-thread="emit('selectThread',$event)" @locate="locate" />
   </div>
  </template>
 </div>
</template>
<style scoped>
.workbench-header,.workbench-actions,.node-tools,.selection-tools{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.workbench-header{justify-content:space-between}.workbench-header h2{font-size:28px}.document-meta{display:flex;gap:7px;flex-wrap:wrap;margin:16px 0 9px}.document-meta span{border:1px solid #2d4d43;background:#11251d;color:#bdded1;border-radius:5px;font-size:11px;padding:4px 8px}.document-summary{font-size:13px;color:#bbd0c5;line-height:1.8;margin-bottom:16px;max-width:1100px}.universal-columns{display:grid;grid-template-columns:minmax(0,1fr) 330px;gap:18px;margin-top:20px}.universal-map{min-width:0}.node-tools{justify-content:space-between;margin-bottom:12px}.node-tools h3{font-size:16px;margin:0}.node-tools input{min-width:0;width:230px;max-width:100%;border:1px solid #345470;border-radius:6px;padding:8px;background:#101d2b}.plan-node-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(195px,1fr));gap:9px;max-height:330px;overflow:auto;padding:2px}.plan-node{display:flex;flex-direction:column;gap:6px;min-width:0;text-align:left;padding:13px;border:1px solid #29473d;border-radius:9px;background:#101e19;overflow-wrap:anywhere}.plan-node[aria-pressed=true]{border-color:#7bdcbe;background:#19372b}.plan-node small{font-size:10px;color:#a0bcaf}.plan-node strong{font-size:14px}.plan-node p{font-size:11px;color:#b0c6bb;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}.plan-node span{font-size:10px;color:#8ec6b1;margin-top:auto}.selection-tools{margin:15px 0}.selection-tools label{font-size:11px;display:flex;gap:5px}.node-evidence,.universal-source,.plan-questions{padding:14px;border:1px solid #29473d;border-radius:8px;margin-top:12px;font-size:12px}.node-evidence ul{padding-left:16px}.node-evidence li{margin:6px 0;overflow-wrap:anywhere}.node-evidence button{background:none;border:0;color:#a0d8fc;padding:0;overflow-wrap:anywhere;text-align:left}.node-evidence li span{font-size:10px;color:#b7b693;margin-left:8px}.universal-source pre{max-height:360px;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;color:#c7deef;background:#0a1420;padding:12px;font-size:11px}.active-project-run{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:10px 14px;border:1px solid #436957;border-radius:8px;color:#a3efd2;background:#132c20;margin-bottom:16px}.plan-questions summary{cursor:pointer}@media(max-width:1200px){.universal-columns{grid-template-columns:minmax(0,1fr)}}@media(max-width:600px){.plan-node-grid{grid-template-columns:minmax(0,1fr)}.node-tools input{width:100%}}
</style>
<style scoped>
.all-node-details,.file-operation-details{margin-top:16px;padding:12px;border:1px solid #29473d;border-radius:8px;font-size:12px}.all-node-details summary,.file-operation-details summary{cursor:pointer;margin-bottom:10px;color:#b4d6c8}
.version-downloads{display:flex;flex-wrap:wrap;gap:8px;margin:10px 0}.version-downloads a{font-size:11px;color:#b4e7d3;border:1px solid #345346;padding:6px 9px;border-radius:5px;text-decoration:none}
</style>
