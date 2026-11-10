<script setup lang="ts">
import { computed, useId } from 'vue'

const props = defineProps<{
  label: string
  overridden: boolean
  defaultText: string
  hint?: string
  busy?: boolean
}>()
const emit = defineEmits<{ reset: [] }>()

const id = useId()
const hintId = computed(() => `${id}-hint`)
</script>

<template>
  <div class="field">
    <div class="row" style="justify-content: space-between">
      <label :for="id">
        {{ label }}
        <span v-if="overridden" class="badge badge-accent" style="margin-left: 0.25rem">{{ $t('admin.settings.overridden') }}</span>
      </label>
      <button v-if="overridden" type="button" class="btn btn-small btn-ghost" :disabled="busy" @click="emit('reset')">
        {{ $t('admin.settings.reset') }}
      </button>
    </div>
    <slot :id="id" :describedby="hintId" />
    <p :id="hintId" class="hint">
      {{ $t('admin.settings.default', { value: defaultText }) }}
      <template v-if="hint"> · {{ hint }}</template>
    </p>
  </div>
</template>
