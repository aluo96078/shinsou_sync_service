<script setup lang="ts">
import { computed, useId } from 'vue'

const props = defineProps<{
  label: string
  hint?: string
  optional?: boolean
}>()

const id = useId()
const hintId = computed(() => (props.hint ? `${id}-hint` : undefined))
</script>

<template>
  <div class="field">
    <label :for="id">
      {{ label }}
      <span v-if="optional" class="muted field-optional">{{ $t('common.optional') }}</span>
    </label>
    <slot :id="id" :describedby="hintId" />
    <p v-if="hint" :id="hintId" class="hint">{{ hint }}</p>
  </div>
</template>
