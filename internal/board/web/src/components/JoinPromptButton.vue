<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import UiIcon from './UiIcon.vue'
import { useBoard } from '../stores/board'
import { buildProjectJoinPrompt, copyText } from '../lib/prompts'

withDefaults(
  defineProps<{ variant?: 'join' | 'toolbar' }>(),
  { variant: 'join' },
)

const board = useBoard()
const toastMessage = ref('')
const toastVisible = ref(false)
let toastTimer: ReturnType<typeof setTimeout> | null = null

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

const text = computed(() => (zh.value ? '接入' : 'Join'))

function showToast(message: string) {
  toastMessage.value = message
  toastVisible.value = true
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    toastVisible.value = false
    toastMessage.value = ''
    toastTimer = null
  }, 2800)
}

async function onCopy() {
  if (!projectId.value && projects.value.length > 1) {
    showToast(zh.value ? '请先选择工程' : 'Pick a project first')
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
    showToast(
      zh.value
        ? '已复制，请到agent对话框中使用'
        : 'Copied — paste it in the agent chat',
    )
  } catch {
    showToast(zh.value ? '复制失败' : 'Copy failed')
  }
}

onBeforeUnmount(() => {
  if (toastTimer) clearTimeout(toastTimer)
})
</script>

<template>
  <button
    type="button"
    id="f-copy-join"
    :class="variant === 'join' ? 'join-button' : 'copy'"
    :disabled="!projects.length"
    :title="title"
    :aria-label="title"
    @click="onCopy"
  >
    <UiIcon name="copy" />{{ text }}
  </button>
  <Teleport to="body">
    <div
      v-if="toastVisible"
      class="ui-toast"
      role="status"
      aria-live="polite"
    >{{ toastMessage }}</div>
  </Teleport>
</template>
