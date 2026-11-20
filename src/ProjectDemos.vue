<script setup lang="ts">
import { ArrowLeft, ArrowUpRight, ChevronRight, Image, Play, RefreshCw } from 'lucide-vue-next'
import { computed, ref, watch } from 'vue'

interface Demo {
  title: string
  path: string
  url: string
  kind: 'html' | 'image'
}

const props = defineProps<{ project: { id: string; name: string; root: string }; initialPath?: string }>()
const demos = ref<Demo[]>([])
const runnable = computed(() => demos.value.filter(demo => demo.kind === 'html'))
const prototypes = computed(() => demos.value.filter(demo => demo.kind === 'image'))
const selected = ref<Demo | null>(null)
const loading = ref(false)
const error = ref('')
const revision = ref(0)

watch([() => props.project.id, () => props.initialPath, revision], async ([id, initialPath], _, onCleanup) => {
  const request = new AbortController()
  onCleanup(() => request.abort())
  demos.value = []
  selected.value = null
  loading.value = true
  error.value = ''
  try {
    const response = await fetch(`/api/projects/${encodeURIComponent(id)}/demos`, { signal: request.signal })
    if (!response.ok) throw new Error('无法读取项目 Demo，请检查本地服务和项目目录。')
    const result = await response.json() as Demo[]
    if (!request.signal.aborted) {
      demos.value = result
      selected.value = result.find(demo => demo.path === initialPath) ?? null
    }
  } catch (cause) {
    if (!request.signal.aborted) error.value = cause instanceof Error ? cause.message : '读取 Demo 失败。'
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}, { immediate: true })
</script>

<template>
  <div class="project-scan demo-workspace">
    <header class="scan-header">
      <div>
        <h2>{{ project.name }}</h2>
        <p class="scan-root">{{ project.root }}</p>
      </div>
      <button v-if="!selected" class="dialog-button scan-reload" type="button" :disabled="loading" @click="revision++">
        <RefreshCw :size="15" aria-hidden="true" />重新扫描
      </button>
    </header>

    <p v-if="loading" class="scan-feedback" role="status">正在查找 Demo…</p>
    <p v-else-if="error" class="scan-feedback form-error" role="alert">{{ error }}</p>
    <section v-else-if="selected" class="demo-preview" aria-labelledby="demo-title">
      <button class="scan-more demo-back" type="button" @click="selected = null"><ArrowLeft :size="16" aria-hidden="true" />返回 Demo</button>
      <div class="demo-preview-bar">
        <div><h3 id="demo-title">{{ selected.title }}</h3><code>{{ selected.path }}</code></div>
        <a v-if="selected.kind === 'html'" class="dialog-button scan-reload" :href="selected.url" target="_blank" rel="noopener noreferrer">
          独立打开<ArrowUpRight :size="15" aria-hidden="true" />
        </a>
      </div>
      <iframe v-if="selected.kind === 'html'" :key="selected.path" :src="selected.url" :title="selected.title" sandbox="allow-scripts" referrerpolicy="no-referrer"></iframe>
      <img v-else :src="selected.url" :alt="`${selected.title}的原型截图`" />
    </section>
    <template v-else>
      <section class="demo-section" aria-labelledby="runnable-title">
        <h3 id="runnable-title">可试玩 Demo</h3>
        <div v-if="runnable.length" class="demo-grid">
          <button v-for="demo in runnable" :key="demo.path" type="button" class="demo-card" @click="selected = demo">
            <Play :size="18" aria-hidden="true" />
            <span><strong>{{ demo.title }}</strong><code>{{ demo.path }}</code></span>
            <ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
        <p v-else class="scan-feedback">暂无可试玩的 Demo。放入 <code>demos/&lt;名称&gt;/index.html</code> 后，点击「重新扫描」。</p>
      </section>
      <section v-if="prototypes.length" class="demo-section" aria-labelledby="prototype-title">
        <h3 id="prototype-title">早期原型截图</h3>
        <div class="demo-grid">
          <button v-for="demo in prototypes" :key="demo.path" type="button" class="demo-card" @click="selected = demo">
            <Image :size="18" aria-hidden="true" />
            <span><strong>{{ demo.title }}</strong><code>{{ demo.path }}</code></span>
            <ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
      </section>
    </template>
  </div>
</template>
