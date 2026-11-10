<script setup lang="ts">
import ErrorNote from './ErrorNote.vue'
import UnverifiedNotice from './UnverifiedNotice.vue'

defineProps<{ unverified: boolean; disabled: boolean; error: string | null }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <UnverifiedNotice v-if="unverified" />
  <section v-else-if="disabled" class="empty-state">
    <h2>{{ $t('reviews.disabledTitle') }}</h2>
    <p>{{ $t('reviews.disabledBody') }}</p>
  </section>
  <template v-else-if="error">
    <ErrorNote :message="error" />
    <button type="button" class="btn" @click="$emit('retry')">{{ $t('common.retry') }}</button>
  </template>
  <slot v-else />
</template>
