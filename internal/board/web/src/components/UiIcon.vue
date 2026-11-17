<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number }>(), { size: 16 })
const paths: Record<string, string> = {"board": "M3 4h18v16H3z M9 4v16 M15 4v16", "factory": "M4 20V10l6 3V8l6 3V4h4v16z M8 17h1 M13 17h1", "execution": "M5 5h14v14H5z M9 9l6 3-6 3z", "topo": "M9 3h6v5H9z M3 16h6v5H3z M15 16h6v5h-6z M12 8v4 M6 16v-4h12v4", "timeline": "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 7v5l3 2", "activity": "M3 12h4l3-7 4 14 3-7h4", "sessions": "M4 4h16v12H9l-5 4z M8 8h8 M8 12h5", "analytics": "M4 20V4 M4 20h16 M8 16v-4 M13 16V8 M18 16V5", "problems": "M12 3l10 18H2z M12 9v5 M12 17h.01", "logs": "M5 4h14v16H5z M8 8h8 M8 12h8 M8 16h5", "projects": "M3 7V4h6l2 3h10v13H3z", "nodes": "M4 3h16v7H4z M4 14h16v7H4z M8 6h.01 M8 17h.01", "settings": "M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6", "search": "M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15 M16 16l5 5", "filters": "M4 6h16 M7 12h10 M10 18h4", "copy": "M8 8h12v13H8z M16 8V3H3v13h5", "menu": "M4 6h16 M4 12h16 M4 18h16", "close": "M6 6l12 12 M18 6L6 18", "deliveries": "M4 7l8-4 8 4v10l-8 4-8-4z M4 7l8 4 8-4 M12 11v10", "tasks": "M9 6h12 M9 12h12 M9 18h12 M3 6h.01 M3 12h.01 M3 18h.01", "runs": "M8 4l12 8-12 8z"}
</script>

<template>
  <svg class="ui-icon" :width="size" :height="size" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"
    aria-hidden="true" focusable="false"><path :d="paths[name] || paths.board" /></svg>
</template>
