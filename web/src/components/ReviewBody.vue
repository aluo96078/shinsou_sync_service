<script setup lang="ts">
import { ref } from 'vue'

// bodyHtml is made by the server from escaped input and holds a fixed set of attribute-free
// tags only (docs/spec/reviews.md section 4), so it is inserted as is.
defineProps<{ bodyHtml: string; spoiler?: boolean }>()
const open = ref(false)
</script>

<template>
  <div v-if="bodyHtml !== '' && spoiler && !open" class="spoiler-fold">
    <span class="muted">{{ $t('reviews.spoilerFolded') }}</span>
    <button type="button" class="btn btn-small" aria-expanded="false" @click="open = true">
      {{ $t('reviews.showSpoiler') }}
    </button>
  </div>
  <template v-else-if="bodyHtml !== ''">
    <div class="md" v-html="bodyHtml"></div>
    <button v-if="spoiler" type="button" class="btn btn-small btn-ghost" aria-expanded="true" @click="open = false">
      {{ $t('reviews.hideSpoiler') }}
    </button>
  </template>
</template>
