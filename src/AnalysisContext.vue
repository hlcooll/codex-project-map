<script setup lang="ts">
import { computed } from 'vue'
import type { PlanState } from './projectPlan'

const props = defineProps<{ state: PlanState }>()
const running = computed(() => props.state.runs.find(run => run.role === 'plan' && ['starting', 'running'].includes(run.status)))
const context = computed(() => running.value?.context ?? props.state.analysisContext)
const skill = computed(() => context.value?.skill)
const gaps = computed(() => props.state.analysis?.gaps ?? [])
const threadId = computed(() => running.value?.threadId || props.state.plannerThreadId)
const hasOldHistory = computed(() => props.state.runs.some(run => run.role === 'plan' && run.context?.owner !== 'workbench'))
const adapterName: Record<string, string> = { swift: 'Swift / Xcode', web: 'Web', go: 'Go 后端' }
const gapName = { adapter: '技术栈适配', scanner: '源码扫描', preview: '界面预览' }
</script>

<template>
  <section v-if="context" class="analysis-context" aria-label="分析上下文">
    <details>
      <summary>
        <strong>分析会话 · {{ context.owner === 'workbench' ? '工作台' : '目标项目' }}</strong>
        <span class="scope">{{ running ? '本轮使用' : '下次分析使用' }}</span>
        <span v-if="skill" class="tag">{{ skill.name }} · v{{ skill.version }}</span>
        <span v-for="adapter in skill?.adapters" :key="adapter" class="tag">{{ adapterName[adapter] || adapter }}</span>
        <span v-if="skill && !skill.adapters.length" class="tag">通用源码分析</span>
        <span class="expand-hint">目录与版本详情</span>
      </summary>
      <dl class="context-fields">
        <dt>会话目录</dt><dd><code>{{ context.cwd }}</code></dd>
        <dt>只读目标</dt><dd><code>{{ context.targetRoot }}</code></dd>
        <dt>会话 ID</dt><dd><code v-if="threadId">{{ threadId }}</code><span v-else>发起分析后创建，每个目标项目独立保存。</span></dd>
        <template v-if="skill"><dt>技能摘要</dt><dd><code>{{ skill.digest }}</code></dd></template>
      </dl>
      <p v-if="!threadId && hasOldHistory" class="context-note">旧分析会话和档案保留。下次分析会创建工作台会话，并按当前技能重新复核文件。</p>
      <p class="context-note">本轮固定技能与适配器；已保存的图谱和覆盖率来自各自的历史分析。开发任务仍在目标项目目录执行。</p>
    </details>

    <details v-if="gaps.length" class="gaps">
      <summary><strong>适配缺口 · {{ gaps.length }} 项待验证</strong><span class="expand-hint">查看证据与改进建议</span></summary>
      <p class="context-note">以下建议尚未发布为通用规则。需完成对应实现和验证，再用于新的分析版本。</p>
      <article v-for="gap in gaps" :key="gap.id" class="gap">
        <h4><span>{{ gapName[gap.kind] }}</span>{{ gap.summary }}</h4>
        <p><b>改进建议</b>{{ gap.proposal }}</p>
        <p><b>验证方法</b>{{ gap.verification }}</p>
        <ul><li v-for="(proof, index) in gap.evidence" :key="index"><code>{{ proof.file }}:{{ proof.line }}</code><span>{{ proof.match }}</span><small v-if="!proof.valid">{{ proof.reason || '证据待核对' }}</small></li></ul>
        <p class="provenance">发现于技能 {{ gap.skillVersion || '未记录' }} · 任务 {{ gap.runId || '未记录' }}</p>
      </article>
    </details>
  </section>
</template>

<style scoped>
.analysis-context { margin: 0 0 14px; border: 1px solid #2d4d43; border-radius: 9px; background: #0e1d17; color: #bdd8cc; font-size: 12px; overflow-wrap: anywhere; }
details { padding: 12px 14px; }
summary { cursor: pointer; line-height: 2; }
summary strong { color: #d4f1e4; font-size: 13px; margin-right: 12px; }
.scope { color: #9aafa5; margin-right: 7px; }
.tag { display: inline-block; margin: 2px 5px 2px 0; padding: 0 7px; background: #193128; border: 1px solid #345547; border-radius: 4px; color: #a3dfc8; font-size: 11px; }
.expand-hint { color: #99b8a9; margin-left: 10px; font-size: 11px; }
.context-fields { display: grid; grid-template-columns: 80px minmax(0,1fr); gap: 8px 12px; margin: 14px 0; }
dt { color: #8fae9e; } dd { margin: 0; min-width: 0; } code { font-size: 11px; }
.context-note { color: #9eb5a9; line-height: 1.7; margin: 9px 0 0; }
.gaps { border-top: 1px solid #2d4d43; }
.gap { border-top: 1px solid #2b4238; margin-top: 12px; padding-top: 12px; }
.gap h4 { margin: 0 0 8px; color: #ecd6a0; font-size: 13px; }
.gap h4 span { color: #a4c4b5; margin-right: 10px; font-size: 11px; font-weight: 400; }
.gap p { line-height: 1.7; margin: 5px 0; }.gap b { color: #a4c4b5; margin-right: 10px; font-weight: 400; }
.gap ul { padding-left: 18px; }.gap li { margin: 6px 0; }.gap li span,.gap li small { display: block; color: #9eb5a9; margin-top: 3px; }
.gap .provenance { color: #829f90; font-size: 10px; }
@media(max-width:640px) { .context-fields { grid-template-columns: 1fr; gap: 4px; } dd { margin-bottom: 7px; } summary strong { display: inline; } .expand-hint { display: block; margin-left: 0; } }
</style>
