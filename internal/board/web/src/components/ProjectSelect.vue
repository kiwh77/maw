<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'

const board = useBoard()
const projects = computed(() => board.state.snapshot?.projects || [])
const projectId = computed(() => board.state.route.project || '')
const counts = computed(() => board.state.projectTaskCounts || {})

const options = computed(() =>
  projects.value.map((p) => {
    const n = counts.value[p.id] || 0
    const name = p.name || p.id
    const mark =
      n > 0
        ? board.state.lang === 'zh'
          ? ` · ${n} 任务`
          : ` · ${n} tasks`
        : ''
    return { id: p.id, label: name + mark, hasTasks: n > 0, count: n }
  }),
)

function onChange(event: Event) {
  board.setProject((event.target as HTMLSelectElement).value || '')
}
</script>

<template>
  <label class="project-select" id="project-nav">
    <span class="project-select__label">{{ board.state.lang === 'zh' ? '工程' : 'Project' }}</span>
    <select
      id="f-project"
      class="project-select__control"
      :value="projectId"
      aria-label="Project"
      @change="onChange"
    >
      <option value="">{{ board.state.lang === 'zh' ? '全部工程' : 'All projects' }}</option>
      <option
        v-for="opt in options"
        :key="opt.id"
        :value="opt.id"
        :data-project="opt.id"
        :data-has-tasks="opt.hasTasks ? '1' : '0'"
      >
        {{ opt.hasTasks ? '● ' : '' }}{{ opt.label }}
      </option>
    </select>
  </label>
</template>
