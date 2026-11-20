<script setup lang="ts">
import { Blocks, FileCode2, FileText, Folder, Layers3, RefreshCw } from 'lucide-vue-next'
import { ref, watch } from 'vue'

interface Card {
  name: string
  detail?: string
  source?: string
  paths: string[]
}

interface Overview {
  kind: string
  stack: string[]
  summary?: string
  readme?: string
  architecture: Card[]
  content: { path: string; kind: string; directory: boolean }[]
  features: Card[]
  partial: boolean
}

const props = defineProps<{ project: { id: string; name: string; root: string } }>()
const overview = ref<Overview | null>(null)
const error = ref('')
const loading = ref(false)
const showAllContent = ref(false)
const scan = ref(0)
const visibleContentCount = 8

watch([() => props.project.id, scan], async ([id], _, onCleanup) => {
  const request = new AbortController()
  onCleanup(() => request.abort())
  overview.value = null
  loading.value = true
  error.value = ''
  showAllContent.value = false
  try {
    const response = await fetch(`/api/projects/${encodeURIComponent(id)}/overview`, { signal: request.signal })
    if (!response.ok) {
      const detail = [404, 422].includes(response.status) ? (await response.text()).trim() : ''
      throw new Error(detail || '暂时无法识别项目，请检查本地服务。')
    }
    const result = await response.json() as Overview
    if (!request.signal.aborted) overview.value = result
  } catch (cause) {
    if (!request.signal.aborted) error.value = cause instanceof Error ? cause.message : '读取项目失败。'
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}, { immediate: true })
</script>

<template>
  <div class="project-scan">
    <header class="scan-header">
      <div>
        <h2>{{ project.name }}</h2>
        <p class="scan-root">{{ project.root }}</p>
      </div>
      <button class="dialog-button scan-reload" type="button" :disabled="loading" @click="scan++">
        <RefreshCw :size="15" aria-hidden="true" />重新识别
      </button>
    </header>

    <p v-if="loading" class="scan-feedback" role="status">正在识别项目…</p>
    <p v-else-if="error" class="scan-feedback form-error" role="alert">{{ error }}</p>
    <template v-else-if="overview">
      <div class="scan-meta">
        <strong>{{ overview.kind }}</strong>
        <span v-for="stack in overview.stack" :key="stack">{{ stack }}</span>
      </div>
      <p v-if="overview.summary" class="scan-summary">
        {{ overview.summary }} <small>来源 {{ overview.readme }}</small>
      </p>

      <div class="scan-columns">
        <section class="scan-panel" aria-labelledby="architecture-heading">
          <h3 id="architecture-heading"><Layers3 :size="18" aria-hidden="true" />项目架构</h3>
          <div v-if="overview.architecture.length" class="scan-card-grid">
            <article v-for="card in overview.architecture" :key="card.name" class="scan-card">
              <h4>{{ card.name }}</h4>
              <p v-if="card.detail">{{ card.detail }}</p>
              <ul class="scan-paths" aria-label="识别依据">
                <li v-for="source in card.paths" :key="source"><code>{{ source }}</code></li>
              </ul>
            </article>
          </div>
          <p v-else class="scan-feedback">未识别到常见的构建配置或模块目录。</p>
        </section>

        <section class="scan-panel" aria-labelledby="content-heading">
          <h3 id="content-heading"><Folder :size="18" aria-hidden="true" />项目内容</h3>
          <ul v-if="overview.content.length" class="scan-content">
            <li v-for="entry in (showAllContent ? overview.content : overview.content.slice(0, visibleContentCount))" :key="entry.path">
              <component :is="entry.directory ? Folder : FileText" :size="15" aria-hidden="true" />
              <code>{{ entry.path }}</code>
              <span>{{ entry.kind }}</span>
            </li>
          </ul>
          <p v-else class="scan-feedback">项目目录中暂无可展示的文件。</p>
          <button v-if="overview.content.length > visibleContentCount" class="scan-more" type="button" :aria-expanded="showAllContent" @click="showAllContent = !showAllContent">
            {{ showAllContent ? '收起' : `展开其余 ${overview.content.length - visibleContentCount} 项` }}
          </button>
        </section>
      </div>

      <section class="scan-panel scan-features" aria-labelledby="features-heading">
        <h3 id="features-heading"><Blocks :size="18" aria-hidden="true" />项目功能</h3>
        <div v-if="overview.features.length" class="scan-card-grid">
          <article v-for="(card, index) in overview.features" :key="`${card.name}-${index}`" class="scan-card">
            <h4>{{ card.name }}</h4>
            <p v-if="card.detail">{{ card.detail }}</p>
            <div class="scan-evidence">
              <span><FileCode2 :size="14" aria-hidden="true" />{{ card.source }}</span>
              <ul class="scan-paths" aria-label="来源路径">
                <li v-for="source in card.paths" :key="source"><code>{{ source }}</code></li>
              </ul>
            </div>
          </article>
        </div>
        <p v-else-if="overview.kind === '多项目目录'" class="scan-feedback">通过「+」添加一个子项目目录，即可识别该项目的功能。</p>
        <p v-else class="scan-feedback">未找到可识别的功能文档、页面或接口入口。</p>
      </section>
      <p v-if="overview.partial" class="scan-feedback">目录较大或部分内容不可读取，当前显示可识别的部分。</p>
    </template>
  </div>
</template>
