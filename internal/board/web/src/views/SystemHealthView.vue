<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useBoard } from '../stores/board'
import { buildHealthView, copyFor, scopeText } from '../lib/system-health'

/**
 * 自愈统计视图（SH-503）。
 *
 * 读 SH-502 的 `/api/v1/system/health`：有工程走工程作用域，没有工程读全局聚合。
 * 契约 `maw.lead.selfheal/v1` §6 的 12 项指标在这里出卡；`null` 与
 * `quality=unknown` 一律写「未知」，绝不画成 0。本视图只读，不提供作业操作按钮。
 */
const board = useBoard()

const payload = ref<unknown>(null)
const loading = ref(true)

const lang = computed(() => (board.state.lang === 'en' ? 'en' : 'zh'))
const project = computed(() => String(board.state.route.project || '').trim())
const copy = computed(() => copyFor(lang.value))
const scope = computed(() => scopeText(lang.value, project.value))
const view = computed(() => buildHealthView(payload.value, lang.value))
const errorMessage = computed(() => (view.value.state === 'error' ? view.value.message : ''))
const unsupportedMessage = computed(() =>
  view.value.state === 'unsupported' ? view.value.message : '',
)
const ready = computed(() => (view.value.state === 'ready' ? view.value : null))

async function load() {
  loading.value = true
  payload.value = await board.fetchSystemHealth()
  loading.value = false
}

onMounted(load)
// 工程筛选联动：切工程就按新作用域重取，不复用上一工程的报表。
watch(project, load)
</script>

<template>
  <section class="heal" id="system-health-view" :aria-label="copy.title">
    <div class="heal-head">
      <div>
        <h2 class="heal-title">{{ copy.title }}</h2>
        <p class="heal-sub">{{ copy.subtitle }}</p>
      </div>
      <span class="heal-scope" id="system-health-scope" :data-project="project || 'all'">
        {{ scope }}
      </span>
    </div>

    <div v-if="loading" class="heal-status" id="system-health-loading" role="status">
      {{ copy.loading }}
    </div>

    <div v-else-if="errorMessage" class="heal-status heal-status--error" id="system-health-error" role="alert">
      <p>{{ errorMessage }}</p>
      <button type="button" class="heal-retry" id="system-health-retry" @click="load">
        {{ copy.retry }}
      </button>
    </div>

    <div v-else-if="unsupportedMessage" class="heal-status" id="system-health-unsupported" role="status">
      {{ unsupportedMessage }}
    </div>

    <template v-else-if="ready">
      <div v-if="ready.unknownNotes.length" class="heal-notice" id="system-health-unknowns" role="status">
        <strong>{{ copy.partial }}</strong>
        <ul>
          <li v-for="note in ready.unknownNotes" :key="note">{{ note }}</li>
        </ul>
      </div>

      <div class="heal-meta" id="system-health-meta">
        <span>{{ ready.windowText }}</span>
        <span>{{ ready.generatedText }}</span>
        <span>{{ ready.evidenceText }}</span>
      </div>

      <div class="heal-cards" id="system-health-cards">
        <article
          v-for="card in ready.cards"
          :key="card.key"
          class="heal-card"
          :data-metric="card.key"
          :data-quality="card.unknown ? 'unknown' : 'observed'"
          :data-tone="card.tone"
        >
          <h3 class="heal-card__label">{{ card.label }}</h3>
          <div class="heal-card__value" :class="{ 'is-unknown': card.unknown }">
            {{ card.value }}<span v-if="card.unit" class="heal-card__unit">{{ card.unit }}</span>
          </div>
          <p class="heal-card__detail">{{ card.detail }}</p>
        </article>
      </div>

      <div class="heal-tables" id="system-health-tables">
        <section
          v-for="table in ready.tables"
          :key="table.key"
          class="heal-block"
          :data-table="table.key"
        >
          <div class="heal-block__head">
            <h3>{{ table.title }}</h3>
            <span class="heal-block__basis">{{ table.basis }}</span>
          </div>
          <table v-if="table.rows.length" class="heal-table">
            <thead>
              <tr>
                <th scope="col">{{ copy.nameColumn }}</th>
                <th scope="col" class="heal-num">{{ copy.countColumn }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in table.rows" :key="row.key" :data-row="row.code">
                <td class="heal-code">{{ row.label }}<span class="heal-code__raw">{{ row.code }}</span></td>
                <td class="heal-num">{{ row.count }}</td>
              </tr>
            </tbody>
          </table>
          <p v-else class="heal-block__empty">{{ copy.noRows }}</p>
        </section>
      </div>
    </template>
  </section>
</template>
