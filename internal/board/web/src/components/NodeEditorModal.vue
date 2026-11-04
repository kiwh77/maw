<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useBoard } from '../stores/board'
import { NODE_PRESETS } from '../../../assets/views/nodes/nodes-view.mjs'

const board = useBoard()
const zh = computed(() => board.state.lang !== 'en')

type FormState = {
  id: string
  name: string
  entry: string
  slots: number
  auto_probe: boolean
  heartbeat_interval_sec: number
  default_model: string
}

const form = reactive<FormState>({
  id: '',
  name: '',
  entry: '',
  slots: 1,
  auto_probe: true,
  heartbeat_interval_sec: 60,
  default_model: '',
})

const open = computed(() => Boolean(board.state.nodeModal))
const mode = computed(() => board.state.nodeModal?.mode || 'create')
const editing = computed(() => mode.value === 'edit')

const title = computed(() => {
  if (editing.value) return zh.value ? '编辑节点' : 'Edit node'
  return zh.value ? '新增执行节点' : 'Add execution node'
})

function applyPreset(kind: 'claude' | 'codex' | 'custom') {
  const p = NODE_PRESETS[kind] || NODE_PRESETS.custom
  if (kind === 'custom' && editing.value) return
  form.id = String(p.id || form.id || '')
  form.name = String(p.name || form.name || '')
  form.entry = String(p.entry || form.entry || '')
  form.slots = Number(p.slots) > 0 ? Number(p.slots) : 1
  if (kind === 'claude') form.default_model = form.default_model || 'sonnet'
  if (kind === 'codex') form.default_model = form.default_model || 'gpt-5'
  board.applyNodePreset(kind)
}

watch(
  () => board.state.nodeModal,
  (modal) => {
    if (!modal) return
    const draft = modal.draft || ({} as FormState)
    form.id = String(draft.id || '')
    form.name = String(draft.name || '')
    form.entry = String(draft.entry || '')
    form.slots = Number(draft.slots) > 0 ? Number(draft.slots) : 1
    form.auto_probe = draft.auto_probe !== false
    form.heartbeat_interval_sec =
      Number(draft.heartbeat_interval_sec) >= 5 ? Number(draft.heartbeat_interval_sec) : 60
    form.default_model = String(draft.default_model || '')
  },
  { immediate: true },
)

function close() {
  board.closeNodeModal()
}

async function submit() {
  const id = form.id.trim()
  const entry = form.entry.trim()
  if (!id || !entry) return
  await board.registerNode({
    id,
    name: form.name.trim() || id,
    entry,
    slots: form.slots > 0 ? form.slots : 1,
    auto_probe: form.auto_probe,
    heartbeat_interval_sec: form.heartbeat_interval_sec,
    default_model: form.default_model.trim(),
  })
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="nodes-modal-mask"
      @click="close"
    />
    <div
      v-if="open"
      class="nodes-modal"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
      @keydown.esc.prevent="close"
    >
      <div class="nodes-modal__head">
        <h2>{{ title }}</h2>
        <button type="button" class="nodes-modal__close" :aria-label="zh ? '关闭' : 'Close'" @click="close">
          ×
        </button>
      </div>
      <div class="nodes-modal__body">
        <p class="field-hint">
          {{
            zh
              ? '登记本机 CLI。自动探测按间隔刷新心跳；默认模型在受管启动时注入。'
              : 'Register a local CLI. Auto-probe refreshes heartbeat; default model is injected on managed launch.'
          }}
        </p>
        <div v-if="!editing" class="nodes-modal__presets">
          <button
            type="button"
            class="button"
            :class="{ 'is-active': board.state.nodeFormPreset === 'claude' }"
            @click="applyPreset('claude')"
          >
            Claude
          </button>
          <button
            type="button"
            class="button"
            :class="{ 'is-active': board.state.nodeFormPreset === 'codex' }"
            @click="applyPreset('codex')"
          >
            Codex
          </button>
          <button
            type="button"
            class="button"
            :class="{ 'is-active': board.state.nodeFormPreset === 'custom' }"
            @click="applyPreset('custom')"
          >
            {{ zh ? '自定义' : 'Custom' }}
          </button>
        </div>
        <label class="field">
          <span class="field-label">{{ zh ? '节点 ID' : 'Node ID' }}</span>
          <input
            v-model="form.id"
            class="input"
            required
            pattern="[a-z][a-z0-9_-]{1,63}"
            :readonly="editing"
            placeholder="claude"
          >
        </label>
        <label class="field">
          <span class="field-label">{{ zh ? '显示名' : 'Display name' }}</span>
          <input v-model="form.name" class="input" placeholder="Claude Code">
        </label>
        <label class="field">
          <span class="field-label">{{ zh ? '可执行文件路径' : 'Executable path' }}</span>
          <input
            v-model="form.entry"
            class="input"
            required
            :placeholder="zh ? '/usr/local/bin/claude 或 PATH 中的命令名' : 'Absolute path or PATH command'"
          >
        </label>
        <label class="field">
          <span class="field-label">{{ zh ? '槽数' : 'Slots' }}</span>
          <input v-model.number="form.slots" class="input" type="number" min="1" max="32">
        </label>
        <label class="field nodes-modal__check">
          <input v-model="form.auto_probe" type="checkbox">
          <span>
            <strong>{{ zh ? '自动探测（心跳）' : 'Auto-probe (heartbeat)' }}</strong>
            <span class="field-hint">
              {{
                zh
                  ? '开启后按下方间隔检查入口是否可用，并刷新 last_seen'
                  : 'When on, checks entry availability on an interval and refreshes last_seen'
              }}
            </span>
          </span>
        </label>
        <label class="field">
          <span class="field-label">{{ zh ? '心跳间隔（秒）' : 'Heartbeat interval (sec)' }}</span>
          <input
            v-model.number="form.heartbeat_interval_sec"
            class="input"
            type="number"
            min="5"
            max="3600"
            :disabled="!form.auto_probe"
          >
        </label>
        <label class="field">
          <span class="field-label">{{ zh ? '默认模型' : 'Default model' }}</span>
          <input
            v-model="form.default_model"
            class="input"
            :placeholder="zh ? '如 sonnet / gpt-5；执行任务时默认使用' : 'e.g. sonnet / gpt-5'"
          >
          <span class="field-hint">
            {{
              zh
                ? '受管启动时若 argv 未带 --model/-m，将自动注入该模型'
                : 'Injected as --model / -m on managed launch when argv omits a model flag'
            }}
          </span>
        </label>
        <p v-if="board.state.removeNotice" class="field-hint" :data-kind="board.state.removeNotice.kind">
          {{ board.state.removeNotice.text }}
        </p>
      </div>
      <div class="nodes-modal__foot">
        <button type="button" class="button" @click="close">
          {{ zh ? '取消' : 'Cancel' }}
        </button>
        <button type="button" class="button button--primary" @click="submit">
          {{ editing ? (zh ? '保存' : 'Save') : (zh ? '登记' : 'Register') }}
        </button>
      </div>
    </div>
  </Teleport>
</template>
