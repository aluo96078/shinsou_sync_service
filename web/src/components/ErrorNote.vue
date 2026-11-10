<script setup lang="ts">
defineProps<{ message: string | null | undefined; kind?: 'error' | 'success' | 'info' }>()
</script>

<template>
  <p
    v-if="message"
    class="note"
    :class="`note-${kind ?? 'error'}`"
    :role="(kind ?? 'error') === 'error' ? 'alert' : 'status'"
  >
    {{ message }}
  </p>
</template>
