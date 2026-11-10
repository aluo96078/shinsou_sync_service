<script setup lang="ts">
defineProps<{ value: number | null }>()
</script>

<template>
  <span v-if="value" class="stars" role="img" :aria-label="$t('reviews.stars', { n: value })">
    <span aria-hidden="true">{{ '★'.repeat(value) }}<span class="stars-off">{{ '★'.repeat(5 - value) }}</span></span>
  </span>
  <span v-else class="muted">{{ $t('reviews.noRating') }}</span>
</template>
