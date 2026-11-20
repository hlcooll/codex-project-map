<script setup lang="ts">
import { Folder, Plus } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import ProjectCards from './ProjectCards.vue'
import ProjectDemos from './ProjectDemos.vue'
import ProjectAppUI from './ProjectAppUI.vue'
import ProjectWorkspace from './ProjectWorkspace.vue'
import { useProjectActivity, type LiveTarget } from './liveActivity'

interface Project {
  id: string
  name: string
  root: string
  kind: string
}

const projects = ref<Project[]>([])
const activeProjectId = ref('')
const activeProject = computed(() => projects.value.find(project => project.id === activeProjectId.value))
const projectSection = ref<'workbench' | 'app-ui' | 'demos' | 'details'>('workbench')
const { snapshot, connected, selectedThread, visibleEvents, pulses } = useProjectActivity(() => activeProjectId.value)
const appTarget = ref<LiveTarget | null>(null)
const planTarget = ref<LiveTarget | null>(null)
function locateApp(target: LiveTarget) { appTarget.value = { ...target }; projectSection.value = 'app-ui' }
function locatePlan(target: LiveTarget) { planTarget.value = { ...target }; projectSection.value = 'workbench' }
const linkedDemo = ref('')
const loadingProjects = ref(true)
const listError = ref(false)
const projectDialog = ref<HTMLDialogElement | null>(null)
const projectPath = ref('')
const projectError = ref('')
const addingProject = ref(false)

async function loadProjects() {
  loadingProjects.value = true
  listError.value = false
  try {
    const response = await fetch('/api/projects')
    if (!response.ok) throw new Error('无法读取项目列表。')
    projects.value = await response.json() as Project[]
  } catch {
    listError.value = true
  } finally {
    loadingProjects.value = false
  }
}

function openProjectDialog() {
  projectPath.value = ''
  projectError.value = ''
  projectDialog.value?.showModal()
}

function selectProject(id: string) {
  activeProjectId.value = id
  linkedDemo.value = ''
  appTarget.value = null
  planTarget.value = null
  projectSection.value = 'workbench'
}

function openDemo(path: string) {
  linkedDemo.value = path
  projectSection.value = 'demos'
}

async function addProject() {
  if (addingProject.value) return
  const path = projectPath.value.trim()
  if (!path) {
    projectError.value = '请输入项目目录。'
    return
  }

  addingProject.value = true
  projectError.value = ''
  try {
    const response = await fetch('/api/projects', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path }),
    })
    if (!response.ok) {
      const detail = response.status === 400 ? (await response.text()).trim() : ''
      throw new Error(detail || '无法添加项目，请检查本地服务。')
    }
    const project = await response.json() as Project
    const existing = projects.value.findIndex(item => item.id === project.id)
    if (existing === -1) projects.value.push(project)
    else projects.value[existing] = project
    selectProject(project.id)
    projectSection.value = 'workbench'
    projectDialog.value?.close()
  } catch (error) {
    projectError.value = error instanceof Error ? error.message : '添加项目失败。'
  } finally {
    addingProject.value = false
  }
}

onMounted(loadProjects)
</script>

<template>
  <div class="app-shell">
    <aside class="project-sidebar" aria-labelledby="projects-heading">
      <div class="project-sidebar-heading">
        <h1 id="projects-heading">项目</h1>
        <button class="add-project" type="button" aria-label="添加项目" title="添加项目" :disabled="loadingProjects || listError" @click="openProjectDialog">
          <Plus :size="17" aria-hidden="true" />
        </button>
      </div>
      <p v-if="loadingProjects" class="list-message" role="status">正在读取项目</p>
      <div v-else-if="listError" class="list-message">
        <p role="alert">无法读取项目列表</p>
        <button class="dialog-button" type="button" @click="loadProjects">重试</button>
      </div>
      <ul v-else-if="projects.length" class="project-list">
        <li v-for="project in projects" :key="project.id">
          <button class="project-entry" type="button" :aria-pressed="activeProjectId === project.id" :title="project.root" @click="selectProject(project.id)">
            <Folder :size="16" aria-hidden="true" />
            <span>{{ project.name }}</span>
          </button>
        </li>
      </ul>
      <p v-else class="list-message">暂无项目</p>
    </aside>

    <main class="workspace" :aria-label="activeProject?.name ?? '工作区'">
      <template v-if="activeProject">
        <nav class="project-sections" aria-label="项目页面">
          <button class="dialog-button" type="button" :aria-pressed="projectSection === 'workbench'" @click="projectSection = 'workbench'">项目规划</button>
          <button v-if="activeProject.kind === '原生 App'" class="dialog-button" type="button" :aria-pressed="projectSection === 'app-ui'" @click="projectSection = 'app-ui'">App 界面</button>
          <button class="dialog-button" type="button" :aria-pressed="projectSection === 'demos'" @click="openDemo('')">Demo</button>
          <button class="dialog-button" type="button" :aria-pressed="projectSection === 'details'" @click="projectSection = 'details'">识别详情</button>
        </nav>
        <ProjectWorkspace v-if="projectSection === 'workbench'" :key="activeProject.id" :project="activeProject" :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :events="visibleEvents" :pulses="pulses" :external-target="planTarget" @select-thread="selectedThread=$event" @app-target="locateApp" @demo="openDemo" />
        <ProjectAppUI v-else-if="projectSection === 'app-ui'" :project="activeProject" :snapshot="snapshot" :connected="connected" :selected-thread="selectedThread" :visible-events="visibleEvents" :pulses="pulses" :external-target="appTarget" @select-thread="selectedThread=$event" @plan-target="locatePlan" @demo="openDemo" />
        <ProjectDemos v-else-if="projectSection === 'demos'" :project="activeProject" :initial-path="linkedDemo" />
        <ProjectCards v-else :project="activeProject" />
      </template>
    </main>

    <dialog ref="projectDialog" class="project-dialog" aria-labelledby="add-project-heading" @cancel="addingProject && $event.preventDefault()">
      <form @submit.prevent="addProject">
        <h2 id="add-project-heading">添加本地项目</h2>
        <label>
          项目目录
          <input v-model="projectPath" required autofocus autocomplete="off" :disabled="addingProject" placeholder="/Users/你的用户名/Projects/my-app" />
        </label>
        <p v-if="projectError" class="form-error" role="alert">{{ projectError }}</p>
        <footer>
          <button class="dialog-button" type="button" :disabled="addingProject" @click="projectDialog?.close()">取消</button>
          <button class="dialog-button primary" type="submit" :disabled="addingProject">{{ addingProject ? '正在添加' : '添加' }}</button>
        </footer>
      </form>
    </dialog>
  </div>
</template>
