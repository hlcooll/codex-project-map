<script setup lang="ts">
import {computed,nextTick,onBeforeUnmount,ref,watch} from 'vue'
import {previewKindLabel,previewSourceLabel,type UIAsset,type PlanCapture,type ProjectUI,type UIPreviewKind} from './projectPlan'
const props=defineProps<{projectId:string;nodeId:string;title:string;assets?:UIAsset[];captures?:PlanCapture[]}>()
const emit=defineEmits<{changed:[]}>()
const modal=ref<HTMLDialogElement|null>(null),opened=ref(false),busy=ref(false),loading=ref(false),error=ref(''),search=ref(''),limit=ref(24)
const assets=ref<UIAsset[]>([]),chosen=ref(''),kind=ref<UIPreviewKind>('reference'),label=ref(''),uploadLabel=ref(''),file=ref<File|null>(null)
const matches=computed(()=>assets.value.filter(a=>`${a.file} ${a.label}`.toLowerCase().includes(search.value.toLowerCase())))
const selected=computed(()=>assets.value.find(a=>a.id===chosen.value))
let controller:AbortController|undefined,generation=0
function close(){generation++;controller?.abort();modal.value?.close();opened.value=false;busy.value=false;loading.value=false}
watch(()=>[props.projectId,props.nodeId],close);watch(search,()=>limit.value=24);onBeforeUnmount(close)
async function open(){
 close();opened.value=true;error.value='';search.value='';limit.value=24;chosen.value='';label.value='';uploadLabel.value='';file.value=null;kind.value='reference';assets.value=props.assets??[];await nextTick();if(!opened.value)return;modal.value?.showModal()
 const gen=generation;const request=new AbortController();controller=request;loading.value=true
 try{const r=await fetch(`/api/projects/${encodeURIComponent(props.projectId)}/ui-assets`,{signal:request.signal});if(!r.ok)throw new Error('素材目录读取失败');const ui=await r.json() as ProjectUI;if(gen===generation)assets.value=ui.assets}catch(e){if(gen===generation&&!request.signal.aborted)error.value=e instanceof Error?e.message:'读取失败'}finally{if(gen===generation)loading.value=false}
}
function choose(asset:UIAsset){chosen.value=asset.id;label.value=asset.label;kind.value=asset.source==='capture'?'capture':'reference'}
function pickFile(event:Event){file.value=(event.target as HTMLInputElement).files?.[0]??null}
async function save(mode:'bind'|'upload'|'remove',assetId=''){
 if(busy.value)return;error.value=''
 if(mode==='bind'&&!selected.value){error.value='先选择一张素材';return}
 if(mode==='upload'&&(!file.value||file.value.size>8*1024*1024||!['image/png','image/jpeg'].includes(file.value.type))){error.value='请选择不超过8MiB的PNG或JPEG截图';return}
 const gen=generation;busy.value=true;controller?.abort();const request=new AbortController();controller=request;let endpoint='ui-preview',method='POST',body:BodyInit|undefined,headers:HeadersInit|undefined
 if(mode==='bind'){body=JSON.stringify({nodeId:props.nodeId,assetId:chosen.value,kind:kind.value,label:label.value});headers={'Content-Type':'application/json'}}
 else if(mode==='upload'){endpoint='ui-capture';const form=new FormData();form.append('nodeId',props.nodeId);form.append('label',uploadLabel.value||`${props.title} · 完整界面`);form.append('file',file.value!);body=form}
 else{method='DELETE';endpoint+=`?${new URLSearchParams({nodeId:props.nodeId,assetId})}`}
 try{const r=await fetch(`/api/projects/${encodeURIComponent(props.projectId)}/${endpoint}`,{method,body,headers,signal:request.signal});if(!r.ok)throw new Error((await r.text()).trim());if(gen===generation){emit('changed');close()}}
 catch(e){if(gen===generation&&!request.signal.aborted)error.value=e instanceof Error?e.message:'预览保存失败'}finally{if(gen===generation)busy.value=false}
}
</script>
<template>
 <button type="button" class="ui-manage-button" :aria-label="`关联预览：${title}`" @click="open">关联预览</button>
 <dialog ref="modal" class="ui-manager" @cancel.prevent="close">
  <template v-if="opened">
   <header><strong>{{title}} · 关联界面预览</strong><button type="button" @click="close">关闭</button></header>
   <p class="ui-hint">页面已与源码图谱关联。选择现有素材，或从运行中的 App／模拟器采集完整界面后上传。</p>
   <p v-if="error" role="alert" class="ui-error">{{error}}</p>
   <section v-if="captures?.length" class="ui-bound"><h4>已关联状态</h4><div v-for="capture in captures" :key="capture.url"><span>{{capture.label}} · {{previewKindLabel[capture.kind??'reference']}} · {{previewSourceLabel(capture.source)}}</span><button v-if="capture.assetId" type="button" :disabled="busy" @click="save('remove',capture.assetId)">移除关联</button></div></section>
   <section class="ui-upload"><h4>上传真实界面截图</h4><p class="ui-hint">PNG / JPEG，最多8MiB。请保留页面控件；纯粒子渲染与海报应在下方标为渲染输出或历史参考。</p><input aria-label="截图文件" type="file" accept="image/png,image/jpeg" :disabled="busy" @change="pickFile"/><input v-model="uploadLabel" aria-label="截图状态名称" placeholder="状态名称，例如：主页、资料卡已打开" maxlength="120"/><button type="button" :disabled="busy||!file" @click="save('upload')">上传并关联</button></section>
   <section><h4>项目素材（{{assets.length}}）</h4><input v-model="search" aria-label="搜索预览素材" type="search" placeholder="搜索文件名或素材名称"/><p v-if="loading" role="status">正在读取素材目录…</p>
    <div class="ui-asset-grid"><button v-for="asset in matches.slice(0,limit)" :key="asset.id" type="button" :aria-pressed="chosen===asset.id" :disabled="busy" @click="choose(asset)"><img :src="asset.url" :alt="asset.label" loading="lazy"/><strong>{{asset.label}}</strong><small>{{asset.file}}</small></button></div>
    <p v-if="!matches.length&&!loading" class="ui-hint">未找到素材。可以上传当前页面的真实截图。</p><button v-if="matches.length>limit" type="button" @click="limit+=24">再显示24张</button>
    <div v-if="selected" class="ui-association"><p>{{selected.file}}</p><details v-if="selected.references.length"><summary>文档引用（{{selected.references.length}}）</summary><p v-for="proof in selected.references" :key="`${proof.file}:${proof.line}`">{{proof.file}}:{{proof.line}} · {{proof.text}}</p></details><label>预览类型 <select v-model="kind"><option value="capture">完整界面截图</option><option value="render">渲染输出（不计入完整界面）</option><option value="reference">历史参考（不计入完整界面）</option></select></label><label>状态名称 <input v-model="label" maxlength="120"/></label><button type="button" :disabled="busy" @click="save('bind')">关联到此页面</button></div>
   </section>
  </template>
 </dialog>
</template>
<style scoped>
.ui-manage-button{font:inherit;font-size:11px;color:#c1e3d1;background:#13291f;border:1px solid #315146;border-radius:5px;padding:4px 7px;cursor:pointer}.ui-manager{box-sizing:border-box;width:min(920px,94vw);max-height:90vh;overflow:auto;background:#0e1d17;color:#d5e9df;border:1px solid #4d7965;border-radius:12px;padding:20px}.ui-manager::backdrop{background:#000b}.ui-manager header,.ui-bound>div{display:flex;justify-content:space-between;gap:10px;align-items:center}.ui-manager section{border-top:1px solid #314b3e;margin-top:18px;padding-top:12px}.ui-manager h4{margin:3px 0 10px}.ui-manager button,.ui-manager input,.ui-manager select{font-size:12px;box-sizing:border-box;max-width:100%;color:#d5e9df;background:#142c21;border:1px solid #3c5d4c;border-radius:5px;padding:8px}.ui-manager button{cursor:pointer}.ui-manager button:disabled{opacity:.5;cursor:default}.ui-hint,.ui-bound{font-size:12px;line-height:1.7;color:#a7c3b3}.ui-error{color:#ffc1b3;font-size:13px}.ui-upload{display:flex;flex-wrap:wrap;gap:10px}.ui-upload h4,.ui-upload p{width:100%;margin-bottom:2px}.ui-asset-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(125px,1fr));gap:9px;margin:12px 0;max-height:350px;overflow:auto}.ui-asset-grid button{display:flex;flex-direction:column;gap:6px;text-align:left;min-width:0}.ui-asset-grid button[aria-pressed=true]{border-color:#92efce;background:#254a37}.ui-asset-grid img{height:130px;width:100%;object-fit:contain;background:#09120e}.ui-asset-grid small{overflow-wrap:anywhere;color:#9fb9ab}.ui-association{padding:12px;background:#132b21;border-radius:8px;display:grid;gap:10px;font-size:12px;overflow-wrap:anywhere}.ui-association label{display:flex;flex-wrap:wrap;gap:8px;align-items:center}.ui-association details p{font-size:11px;line-height:1.7}.ui-bound>div{padding:5px 0;flex-wrap:wrap}@media(max-width:480px){.ui-manager{padding:12px}.ui-asset-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.ui-manager header{align-items:flex-start}.ui-manager input{min-width:0}}
</style>
