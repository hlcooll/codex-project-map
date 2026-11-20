<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { PlanNode, PlanState, ProjectRun, StartProjectRun } from './projectPlan'

const props = defineProps<{
  state: PlanState | null
  selectedNode: PlanNode | null
  busy: boolean
  error: string
}>()

const emit = defineEmits<{
  start: [run: StartProjectRun]
  interrupt: [id: string]
}>()

const planPrompt = ref('')
const executePrompt = ref('')

const activeRun = computed(() =>
  Boolean(props.state?.runs.some((run) => run.status === 'starting' || run.status === 'running')),
)
const planRuns = computed(() => props.state?.runs.filter((run) => run.role === 'plan') ?? [])
const nodeRuns = computed(() =>
  props.state?.runs.filter(
    (run) => run.role === 'execute' && run.nodeId === props.selectedNode?.id,
  ) ?? [],
)

function sharedDisabledReason() {
  if (props.busy) return '上一项请求处理中。'
  if (!props.state) return '尚未读取规划。'
  if (!props.state.connection.available) {
    return props.state.connection.message.trim() || 'Codex 不可用。'
  }
  if (activeRun.value) return '已有 Codex 回合运行中。'
  return ''
}

const planDisabledReason = computed(sharedDisabledReason)
const executeDisabledReason = computed(() => {
  const reason = sharedDisabledReason()
  if (reason) return reason
  if (!executePrompt.value.trim()) return '执行要求为空。'
  return ''
})
const planActionLabel = computed(() =>
  props.state?.plannerThreadId.trim() ? '继续完整分析' : '创建完整分析',
)

const statusText: Record<ProjectRun['status'], string> = {
  starting: '启动中',
  running: '运行中',
  completed: '回合结束 · 待验收',
  failed: '失败',
  interrupted: '已中断',
}

function startPlan() {
  if (planDisabledReason.value) return
  emit('start', { role: 'plan', analysis:'full', prompt: planPrompt.value.trim() })
}

function startExecution() {
  if (!props.selectedNode || executeDisabledReason.value) return
  emit('start', {
    role: 'execute',
    nodeId: props.selectedNode.id,
    prompt: executePrompt.value.trim(),
  })
}

function continueRun(run: ProjectRun) {
  if (!props.selectedNode || executeDisabledReason.value) return
  emit('start', {
    role: 'execute',
    nodeId: props.selectedNode.id,
    prompt: executePrompt.value.trim(),
    continueRunId: run.id,
  })
}

watch(
  () => props.selectedNode?.id,
  () => {
    executePrompt.value = ''
  },
)
</script>

<template>
  <section class="planning-panel">
    <header class="panel-header">
      <div>
        <p class="eyebrow">Codex 规划</p>
        <h2>项目规划与执行</h2>
      </div>
      <div
        class="connection"
        :class="{ unavailable: state && !state.connection.available }"
        role="status"
      >
        <span class="status-dot" aria-hidden="true"></span>
        <span>{{ state ? state.connection.message : '尚未读取 Codex 状态' }}</span>
      </div>
    </header>

    <p v-if="error" class="error" role="alert">{{ error }}</p>

    <form class="work-section" @submit.prevent="startPlan">
      <div class="section-heading">
        <label for="plan-prompt">规划补充要求</label>
        <p>在工作台按项目分别保存分析会话，按文件清单只读分析。</p>
      </div>
      <textarea
        id="plan-prompt"
        v-model="planPrompt"
        maxlength="12000"
        rows="3"
        placeholder="补充约束、关注点或需要进一步分析的内容（可留空）"
      ></textarea>
      <div class="actions">
        <button type="submit" class="primary" :disabled="Boolean(planDisabledReason)">
          {{ planActionLabel }}
        </button>
        <p v-if="planDisabledReason" class="reason">{{ planDisabledReason }}</p>
      </div>

      <details v-if="planRuns.length" class="history">
        <summary>规划历史</summary>
        <article v-for="run in planRuns" :key="run.id" class="run">
          <div class="run-heading">
            <strong>{{ statusText[run.status] }}</strong>
            <button
              v-if="run.status === 'starting' || run.status === 'running'"
              type="button"
              class="quiet"
              :disabled="busy"
              @click="emit('interrupt', run.id)"
            >
              停止
            </button>
          </div>
          <p class="run-context">会话归属：{{ run.context?.owner === 'workbench' ? '工作台' : '目标项目' }} · {{ run.context?.cwd || '旧记录未记录目录' }}</p>
          <p v-if="run.threadId" class="run-context">会话 ID：{{ run.threadId }}</p>
          <p v-if="run.role === 'plan'" class="run-context">{{ run.context?.skill ? `技能 v${run.context.skill.version} · ${run.context.skill.adapters.join(' / ') || '通用源码分析'}` : '旧分析记录 · 未记录技能版本' }}</p>
          <p class="field"><span>要求</span><span>{{ run.prompt.trim() || '留空' }}</span></p>
          <p class="field"><span>回复</span><span>{{ run.reply.trim() || '暂无回复' }}</span></p>
          <p v-if="run.error.trim()" class="field run-error">
            <span>错误</span><span>{{ run.error }}</span>
          </p>
        </article>
      </details>
    </form>

    <form v-if="selectedNode" class="work-section execution" @submit.prevent="startExecution">
      <div class="section-heading">
        <label for="execute-prompt">“{{ selectedNode.title }}”的执行要求</label>
        <p class="warning">开发会话归属目标项目，执行会修改项目文件。</p>
      </div>
      <textarea
        id="execute-prompt"
        v-model="executePrompt"
        maxlength="12000"
        rows="3"
        placeholder="写明本次要完成、调整和验证的具体内容"
      ></textarea>
      <div class="actions">
        <button type="submit" class="primary" :disabled="Boolean(executeDisabledReason)">
          执行此节点
        </button>
        <p v-if="executeDisabledReason" class="reason">{{ executeDisabledReason }}</p>
      </div>

      <details v-if="nodeRuns.length" class="history">
        <summary>此节点的执行历史</summary>
        <article v-for="run in nodeRuns" :key="run.id" class="run">
          <div class="run-heading">
            <strong>{{ statusText[run.status] }}</strong>
            <button
              v-if="run.status === 'starting' || run.status === 'running'"
              type="button"
              class="quiet"
              :disabled="busy"
              @click="emit('interrupt', run.id)"
            >
              停止
            </button>
          </div>
          <p class="run-context">会话归属：{{ run.context?.owner === 'workbench' ? '工作台' : '目标项目' }} · {{ run.context?.cwd || '旧记录未记录目录' }}</p>
          <p v-if="run.threadId" class="run-context">会话 ID：{{ run.threadId }}</p>
          <p class="field"><span>要求</span><span>{{ run.prompt.trim() || '留空' }}</span></p>
          <p class="field"><span>回复</span><span>{{ run.reply.trim() || '暂无回复' }}</span></p>
          <p v-if="run.error.trim()" class="field run-error">
            <span>错误</span><span>{{ run.error }}</span>
          </p>
          <button
            v-if="run.status !== 'starting' && run.status !== 'running'"
            type="button"
            class="continue"
            :disabled="Boolean(executeDisabledReason)"
            @click="continueRun(run)"
          >
            用当前要求继续会话
          </button>
        </article>
      </details>
    </form>
  </section>
</template>

<style scoped>
* { box-sizing: border-box; }
.planning-panel {
  width: 100%;
  max-width: 920px;
  min-width: 0;
  margin: 0 auto;
  color: #e7f4ee;
  overflow-wrap: anywhere;
}
.panel-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 20px;
  margin-bottom: 18px;
}
.eyebrow { margin: 0 0 4px; color: #80ecc9; font-size: 12px; letter-spacing: .12em; }
h2 { margin: 0; font-size: clamp(22px, 4vw, 30px); }
.connection {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  max-width: 360px;
  min-width: 0;
  padding: 9px 12px;
  border: 1px solid #315447;
  border-radius: 8px;
  color: #bfe8d8;
  background: #102219;
  font-size: 13px;
}
.connection.unavailable { border-color: #6b3d3d; color: #ffc2bc; background: #2a1718; }
.status-dot { flex: 0 0 7px; width: 7px; height: 7px; margin-top: 5px; border-radius: 50%; background: #80ecc9; }
.unavailable .status-dot { background: #ff9d95; }
.error { margin: 0 0 16px; padding: 10px 12px; border: 1px solid #7d4545; border-radius: 8px; color: #ffb1aa; background: #2a1718; }
.work-section { min-width: 0; margin-top: 14px; padding: 16px; border: 1px solid #263d35; border-radius: 10px; background: #0d1914; }
.execution { border-color: #544c2d; }
.section-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; margin-bottom: 10px; }
.section-heading label { color: #f1faf5; font-weight: 650; }
.section-heading p { margin: 0; color: #a3bab0; font-size: 13px; text-align: right; }
.section-heading .warning { color: #f3cd75; }
textarea {
  display: block;
  width: 100%;
  min-width: 0;
  resize: vertical;
  padding: 11px 12px;
  border: 1px solid #38564a;
  border-radius: 8px;
  outline: none;
  color: #e7f4ee;
  background: #13241c;
  font: inherit;
  line-height: 1.55;
}
textarea:focus { border-color: #80ecc9; box-shadow: 0 0 0 2px #80ecc933; }
.actions { display: flex; align-items: center; flex-wrap: wrap; gap: 10px; margin-top: 10px; }
button {
  max-width: 100%;
  min-width: 0;
  padding: 9px 13px;
  border: 1px solid #3d5f51;
  border-radius: 7px;
  color: #dff5eb;
  background: #183126;
  font: inherit;
  white-space: normal;
  cursor: pointer;
}
button.primary { border-color: #80ecc9; color: #071b14; background: #80ecc9; font-weight: 700; }
button.quiet { padding: 5px 9px; color: #ffc3bd; border-color: #6b3d3d; background: #2a1718; }
button.continue { margin-top: 10px; }
button:disabled { cursor: not-allowed; color: #80938a; border-color: #30463b; background: #17251e; }
.reason { margin: 0; color: #d4b975; font-size: 13px; }
.history { min-width: 0; margin-top: 16px; border-top: 1px solid #263d35; padding-top: 12px; }
summary { color: #b9d7ca; cursor: pointer; }
.run { min-width: 0; margin-top: 10px; padding: 11px; border: 1px solid #263d35; border-radius: 8px; background: #101e18; }
.run-heading { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 8px; color: #80ecc9; }
.field { display: grid; grid-template-columns: 38px minmax(0, 1fr); gap: 8px; margin: 6px 0; color: #d4e5dd; white-space: pre-wrap; overflow-wrap: anywhere; }
.field > span:first-child { color: #91aaa0; font-size: 12px; }
.run-error { color: #ffaaa3; }
.run-context { margin: 5px 0; color: #9fbcaf; font-size: 11px; overflow-wrap: anywhere; }
@media (max-width: 640px) {
  .panel-header, .section-heading { display: block; }
  .connection { max-width: none; margin-top: 12px; }
  .section-heading p { margin-top: 4px; text-align: left; }
  .actions button { width: 100%; }
  .work-section { padding: 13px; }
}
</style>
