<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { safeCoverUrl } from '@/lib/redirect'

const props = defineProps<{ src: string | null | undefined; title: string; alt?: string }>()

const failed = ref(false)
const url = computed(() => safeCoverUrl(props.src))
const letter = computed(() => Array.from(props.title.trim())[0]?.toUpperCase() ?? '?')

watch(
  () => props.src,
  () => {
    failed.value = false
  },
)
</script>

<template>
  <div class="cover">
    <img
      v-if="url && !failed"
      :src="url"
      :alt="alt ?? ''"
      referrerpolicy="no-referrer"
      loading="lazy"
      decoding="async"
      @error="failed = true"
    />
    <span v-else class="cover-placeholder" aria-hidden="true">{{ letter }}</span>
  </div>
</template>
