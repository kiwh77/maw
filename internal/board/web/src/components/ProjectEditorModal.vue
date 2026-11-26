<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const zh = computed(() => board.state.lang !== 'en')

const form = reactive({
  name: '',
  path: '',
  mainBranch: '',
  maxParallel: '',
})

const localError = ref('')

const open = computed(() => Boolean(board.state.projectModal))
const projectId = computed(() => board.state.projectModal?.projectId || '')
const title = computed(() => (zh.value ? '编辑工程' : 'Edit project'))

const errorText = computed(() => {
  if (localError.value) return localError.value
  const notice = board.state.removeNotice
  return notice && notice.kind === 'error' ? notice.text : ''
})

watch(
  () => board.state.projectModal,
  (modal) => {
    if (!modal) return
    form.name = modal.draft.name
    form.path = modal.draft.path
    form.mainBranch = modal.draft.mainBranch
    form.maxParallel = modal.draft.maxParallel
    localError.value = ''
  },
  { immediate: true },
)

function close() {
  board.closeProjectModal()
}

async function submit() {
  localError.value = ''
  if (!form.name.trim()) {
    localError.value = zh.value ? '工程名称不能为空' : 'Project name is required'
    return
  }
  const refRaw = String(form.maxParallel || '').trim()
  if (refRaw && (!Number.isInteger(Number(refRaw)) || Number(refRaw) < 0)) {
    localError.value = zh.value
      ? 'max_parallel 参考值必须是非负整数'
      : 'max_parallel reference must be a non-negative integer'
    return
  }
  await board.saveProjectEdit({
    name: form.name,
    path: form.path,
    mainBranch: form.mainBranch,
    maxParallel: form.maxParallel,
  })
}
</script>

<template>
  <Teleport to="body">
    <div v-if="open" class="projects-modal-mask" @click="close" />
    <div
      v-if="open"
      class="projects-modal"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      @keydown.esc.prevent="close"
    >
      <div class="projects-modal__head">
        <h2>{{ title }}</h2>
        <button
          type="button"
          class="projects-modal__close"
          :aria-label="zh ? '关闭' : 'Close'"
          @click="close"
        >
          ×
        </button>
      </div>
      <div class="projects-modal__body">
        <p class="field-hint">
          {{
            zh
              ? '基础信息写入本机中枢登记。'
              : 'Basic fields update the local hub registration.'
          }}
        </p>
        <p class="field-hint projects-modal__id">ID · {{ projectId }}</p>

        <div class="projects-modal__grid">
          <label class="field">
            <span class="field-label">{{ zh ? '工程名称' : 'Project name' }}</span>
            <input v-model="form.name" class="input" required>
          </label>
          <label class="field">
            <span class="field-label">{{ zh ? '存放路径' : 'Project path' }}</span>
            <input
              v-model="form.path"
              class="input"
              :placeholder="zh ? '工程目录绝对路径' : 'Absolute project directory'"
            >
          </label>
          <label class="field">
            <span class="field-label">{{ zh ? '主分支' : 'Main branch' }}</span>
            <input v-model="form.mainBranch" class="input" placeholder="main">
          </label>
          <label class="field">
            <span class="field-label">{{ zh ? 'max_parallel 参考值' : 'max_parallel reference' }}</span>
            <input
              v-model="form.maxParallel"
              class="input"
              type="number"
              min="0"
              :placeholder="zh ? '0 表示未设置' : '0 means unset'"
            >
          </label>
        </div>

        <p v-if="errorText" class="projects-modal__error">{{ errorText }}</p>
      </div>
      <div class="projects-modal__foot">
        <button type="button" class="button" @click="close">
          {{ zh ? '取消' : 'Cancel' }}
        </button>
        <button type="button" class="button button--primary" @click="submit">
          {{ zh ? '保存' : 'Save' }}
        </button>
      </div>
    </div>
  </Teleport>
</template>
