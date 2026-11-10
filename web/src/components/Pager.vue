<script setup lang="ts">
import { computed } from 'vue'
import { formatNumber } from '@/i18n'

const props = defineProps<{ total: number; offset: number; limit: number; busy?: boolean }>()
const emit = defineEmits<{ 'update:offset': [offset: number] }>()

const from = computed(() => (props.total === 0 ? 0 : props.offset + 1))
const to = computed(() => Math.min(props.offset + props.limit, props.total))
const hasPrev = computed(() => props.offset > 0)
const hasNext = computed(() => props.offset + props.limit < props.total)
</script>

<template>
  <nav class="pager" :aria-label="$t('common.range', { from: from, to: to, total: total })">
    <span class="muted" aria-live="polite">
      {{ $t('common.range', { from: formatNumber(from), to: formatNumber(to), total: formatNumber(total) }) }}
    </span>
    <span class="pager-buttons">
      <button
        type="button"
        class="btn btn-small"
        :disabled="!hasPrev || busy"
        @click="emit('update:offset', Math.max(0, offset - limit))"
      >
        {{ $t('common.previous') }}
      </button>
      <button
        type="button"
        class="btn btn-small"
        :disabled="!hasNext || busy"
        @click="emit('update:offset', offset + limit)"
      >
        {{ $t('common.next') }}
      </button>
    </span>
  </nav>
</template>
