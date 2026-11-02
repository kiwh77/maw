<script setup lang="ts">
import { computed, ref } from 'vue'
import { useBoard } from '../stores/board'
import { buildProjectJoinPrompt, copyText } from '../lib/prompts'

const props = withDefaults(
  defineProps<{ variant?: 'join' | 'toolbar' }>(),
  { variant: 'join' },
)

const board = useBoard()
const copyLabel = ref('')
let copyTimer: ReturnType<typeof setTimeout> | null = null

const projects = computed(() => board.state.snapshot?.projects || [])
const projectId = computed(() => board.state.route.project || '')
const selectedProject = computed(() => {
  const id = projectId.value
  if (!id) return null
  return projects.value.find((p) => String(p.id) === id) || null
})

const zh = computed(() => board.state.lang !== 'en')

const title = computed(() => {
  if (selectedProject.value) {
    return zh.value
      ? `复制 ${selectedProject.value.name || selectedProject.value.id} 接入提示词`
      : `Copy ${selectedProject.value.name || selectedProject.value.id} join prompt`
  }
  if (projects.value.length > 1) {
    return zh.value ? '请先选择工程' : 'Pick a project first'
  }
  return zh.value ? '复制工程接入提示词' : 'Copy project join prompt'
})

const text = computed(() => {
  if (copyLabel.value) return copyLabel.value
  return zh.value ? '复制工程接入提示词' : 'Copy project join prompt'
})

async function onCopy() {
  if (!projectId.value && projects.value.length > 1) {
    copyLabel.value = zh.value ? '请先选工程' : 'Pick project'
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => {
      copyLabel.value = ''
    }, 2000)
    return
  }
  const target =
    selectedProject.value || (projects.value.length === 1 ? projects.value[0] : null)
  const prompt = buildProjectJoinPrompt(target, {
    agents: board.state.snapshot?.agents || [],
    tasks: board.state.snapshot?.tasks || [],
    lang: board.state.lang,
  })
  try {
    await copyText(prompt)
    copyLabel.value = zh.value ? '已复制提示词' : 'Prompt copied'
  } catch {
    copyLabel.value = zh.value ? '复制失败' : 'Copy failed'
  }
  if (copyTimer) clearTimeout(copyTimer)
  copyTimer = setTimeout(() => {
    copyLabel.value = ''
  }, 2000)
}
</script>

<template>
  <button
    type="button"
    id="f-copy-join"
    :class="variant === 'join' ? 'join-button' : 'copy primary'"
    :disabled="!projects.length"
    :title="title"
    @click="onCopy"
  >
    <span v-if="variant === 'join'" class="symbol" aria-hidden="true">⌘</span>{{ text }}
  </button>
</template>
