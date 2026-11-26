<script setup lang="ts">
import { computed } from 'vue'
import { useBoard } from '../stores/board'
import {
  buildProblemGroups,
  drawerTarget,
  emptyMessage,
} from '../lib/problems.mjs'

/**
 * 问题审查视图（BUX-FE-002）。
 *
 * 直接消费 snapshot.problems —— 后端 BuildProblems 已经把阻塞／冲突／重试／
 * 提问／停滞／依赖等待聚合成带 severity 的只读事实，这里不再另拉一份数据、
 * 也不提供任何处理动作。路由 id 沿用已注册的 `problems`，因此侧栏、深链、
 * 工程筛选与语言切换都不需要改注册表（注册表不在本卡写域内）。
 */
const board = useBoard()
const lang = computed(() => (board.state.lang === 'en' ? 'en' : 'zh'))
const zh = computed(() => lang.value !== 'en')
const project = computed(() => String(board.state.route.project || '').trim())

const data = computed(() =>
  buildProblemGroups(board.state.snapshot?.problems || [], {
    lang: lang.value,
    project: project.value,
  }),
)

const totalText = computed(() => {
  const n = data.value.total
  return zh.value ? `共 ${n} 条问题` : `${n} problems`
})

const countsText = computed(() => {
  const c = data.value.counts
  return zh.value
    ? `高 ${c.high} · 中 ${c.medium} · 低 ${c.low}${c.other ? ` · 未分级 ${c.other}` : ''}`
    : `High ${c.high} · Medium ${c.medium} · Low ${c.low}${c.other ? ` · Unrated ${c.other}` : ''}`
})

const scopeText = computed(() =>
  project.value
    ? zh.value
      ? `当前工程：${project.value}`
      : `Project: ${project.value}`
    : zh.value
      ? '全部工程'
      : 'All projects',
)

function open(problem: Record<string, unknown>) {
  const target = drawerTarget(problem)
  if (!target) return
  board.openTaskDrawer(target.tid, target.pid)
}
</script>

<template>
  <section class="prob-page" id="problems-view" aria-labelledby="problems-total">
    <div class="prob-summary" id="problems-summary">
      <strong id="problems-total" :data-total="data.total">{{ totalText }}</strong>
      <span class="prob-scope" id="problems-scope">{{ scopeText }}</span>
      <span class="prob-counts" id="problems-counts">{{ countsText }}</span>
      <span v-if="data.project && data.allCount !== data.total" class="prob-filtered">
        {{ zh ? `已按工程筛掉 ${data.allCount - data.total} 条` : `${data.allCount - data.total} hidden by project filter` }}
      </span>
    </div>

    <div v-if="!data.groups.length" class="prob-empty" id="problems-empty" role="status">
      <p class="prob-empty__title">{{ emptyMessage(lang, project) }}</p>
      <p class="prob-empty__hint">
        {{ zh
          ? '阻塞、冲突、重试、提问、停滞与依赖等待会随快照刷新自动出现在这里。'
          : 'Blocked, conflict, retry, question, stalled and dependency waits appear here as the snapshot refreshes.' }}
      </p>
    </div>

    <div v-else class="prob-groups">
      <section
        v-for="group in data.groups"
        :key="group.severity"
        class="prob-group"
        :data-severity="group.severity"
      >
        <h3 class="prob-group__head">
          <span class="prob-dot" aria-hidden="true"></span>
          {{ group.label }}
          <span class="prob-count">{{ group.count }}</span>
        </h3>

        <div v-for="bucket in group.kinds" :key="bucket.kind" class="prob-kind" :data-kind="bucket.kind">
          <h4 class="prob-kind__head">{{ bucket.label }}</h4>
          <ul class="prob-rows">
            <li v-for="(problem, i) in bucket.items" :key="problem.project_id + problem.task_id + problem.kind + i">
              <button
                v-if="drawerTarget(problem)"
                type="button"
                class="prob-row clickable"
                :data-problem-task="problem.task_id"
                :data-problem-project="problem.project_id"
                :title="zh ? '打开任务抽屉' : 'Open task drawer'"
                @click="open(problem)"
              >
                <span class="prob-row__id">{{ problem.task_id }}</span>
                <span class="prob-row__project">{{ problem.project_name || problem.project_id || '—' }}</span>
                <span class="prob-row__message">{{ problem.message || '—' }}</span>
                <span class="prob-row__ts">{{ problem.ts || '—' }}</span>
              </button>
              <div v-else class="prob-row is-static">
                <span class="prob-row__id">—</span>
                <span class="prob-row__project">{{ problem.project_name || problem.project_id || '—' }}</span>
                <span class="prob-row__message">{{ problem.message || '—' }}</span>
                <span class="prob-row__ts">{{ problem.ts || '—' }}</span>
              </div>
            </li>
          </ul>
        </div>
      </section>
    </div>
  </section>
</template>
