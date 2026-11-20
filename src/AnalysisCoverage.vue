<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ProjectAnalysis } from './projectPlan'
const props=defineProps<{analysis:ProjectAnalysis|null|undefined;busy:boolean;running:boolean;canAnalyze:boolean}>()
const emit=defineEmits<{scan:[];analyze:[moduleId:string]}>()
const moduleId=ref(''),fileFilter=ref('pending'),query=ref(''),limit=ref(60)
const files=computed(()=>props.analysis?.files.filter(f=>(!moduleId.value||f.moduleId===moduleId.value)&&(fileFilter.value==='all'||(fileFilter.value==='pending'?f.status!=='reviewed':f.status===fileFilter.value))&&f.path.toLowerCase().includes(query.value.toLowerCase()))??[])
const statusLabel={pending:'待分析',reviewed:'已分析此版本',stale:'源码已变化'}
</script>
<template>
 <section class="analysis-coverage" aria-label="全项目分析覆盖">
  <header><div><h3>全项目分析覆盖</h3><p v-if="analysis">{{analysis.reviewed}} / {{analysis.total}} 个源码文件 · {{analysis.modules.length}} 个目录模块</p><p v-else>先建立完整文件清单，再逐模块分析。</p></div><button class="dialog-button" :disabled="busy||running" @click="emit('scan')">{{busy?'处理中…':analysis?'重新扫描文件':'建立全项目清单'}}</button></header>
  <template v-if="analysis">
   <progress :value="analysis.reviewed" :max="Math.max(analysis.total,1)" :aria-label="`已分析 ${analysis.reviewed} 个文件，共 ${analysis.total} 个`"></progress>
   <p class="coverage-note">{{analysis.status==='complete'?'当前扫描清单已逐文件分析并完成跨模块整合。':'完整分析会继续处理未分析或变更的文件，逐批保存，停止后可继续。'}} 文件级覆盖不等于功能已验收。</p>
   <p class="coverage-time">扫描于 {{new Date(analysis.scannedAt).toLocaleString('zh-CN')}} · {{analysis.integrated?'已进行跨模块链路核对':'跨模块链路待核对'}} · 修改代码后重新扫描可识别变更。</p>
   <p v-for="warning in analysis.warnings" :key="warning" class="coverage-warning">{{warning}}</p><p v-if="analysis.truncated" class="coverage-warning">扫描达到资源上限，本清单不代表整个仓库已覆盖。</p>
   <details class="coverage-details"><summary>查看模块与文件覆盖（待分析 {{analysis.total-analysis.reviewed}}）</summary>
    <div class="coverage-modules"><article v-for="module in analysis.modules" :key="module.id"><button class="module-filter" :aria-pressed="moduleId===module.id" @click="moduleId=moduleId===module.id?'':module.id;limit=60"><strong>{{module.title}}</strong><span>{{module.reviewed}} / {{module.total}} · {{module.status==='complete'?'已分析':'待补全'}}</span></button><button class="dialog-button" :disabled="busy||running||!canAnalyze" @click="emit('analyze',module.id)">分析此模块</button></article></div>
    <div class="file-tools"><button v-if="moduleId" class="dialog-button" @click="moduleId=''">全部模块</button><select v-model="fileFilter" aria-label="文件分析状态"><option value="pending">待分析和已变化</option><option value="reviewed">已分析</option><option value="all">全部文件</option></select><input v-model="query" type="search" placeholder="筛选文件路径" aria-label="筛选覆盖文件" /></div>
    <ul class="coverage-files"><li v-for="file in files.slice(0,limit)" :key="file.path"><code>{{file.path}}</code><span>{{statusLabel[file.status]}}</span><small v-if="file.reason">{{file.reason}}</small></li></ul><p v-if="!files.length">当前筛选没有文件。</p><button v-if="files.length>limit" class="dialog-button" @click="limit+=100">再显示 100 项（共 {{files.length}} 项）</button>
   </details>
   <details v-if="analysis.excluded.length" class="coverage-details"><summary>扫描排除范围</summary><p>排除依赖、构建产物、凭据及不可读取内容；下面的数量按被跳过的文件或目录入口计数，未展开的目录不计内部文件。</p><ul><li v-for="entry in analysis.excluded" :key="entry.reason">{{entry.reason}}：{{entry.count}}</li></ul></details>
  </template>
 </section>
</template>
<style scoped>
.analysis-coverage{padding:16px;border:1px solid #315348;background:#10231b;border-radius:10px;margin:16px 0}.analysis-coverage header{display:flex;justify-content:space-between;gap:16px;align-items:center;flex-wrap:wrap}.analysis-coverage h3{margin:0;font-size:16px}.analysis-coverage p{font-size:12px;line-height:1.7}.analysis-coverage progress{display:block;width:100%;height:6px;margin:12px 0;accent-color:#80d9b8}.coverage-note{color:#c4ded2}.coverage-time{color:#8fae9d;font-size:11px!important}.coverage-warning{color:#edcf8f}.coverage-details{margin-top:12px;font-size:12px}.coverage-details summary{cursor:pointer;color:#b5e4d0}.coverage-modules{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:8px;margin:12px 0}.coverage-modules article{display:flex;border:1px solid #315348;border-radius:6px;align-items:center;gap:5px;padding:7px;min-width:0}.module-filter{background:transparent;border:0;display:grid;gap:5px;text-align:left;flex:1;min-width:0;overflow-wrap:anywhere}.module-filter[aria-pressed=true]{color:#92f7d3}.module-filter span{font-size:11px;color:#9fb7ac}.coverage-modules .dialog-button{font-size:11px;padding:6px;flex-shrink:0}.file-tools{display:flex;gap:8px;flex-wrap:wrap;margin:14px 0}.file-tools select,.file-tools input{padding:7px;border:1px solid #315348;border-radius:5px;background:#0b1913;min-width:0;max-width:100%}.coverage-files{padding:0;list-style:none;max-height:320px;overflow:auto}.coverage-files li{display:flex;gap:8px;flex-wrap:wrap;padding:7px;border-bottom:1px solid #274336}.coverage-files code{flex:1;min-width:150px;overflow-wrap:anywhere}.coverage-files small{width:100%;color:#edcf8f}.coverage-files span{font-size:10px;color:#a7c8b6}
</style>
