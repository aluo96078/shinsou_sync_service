<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import ErrorNote from './ErrorNote.vue'
import FormField from './FormField.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title: string
    message?: string
    confirmLabel?: string
    danger?: boolean
    busy?: boolean
    error?: string | null
    /** When set, the user has to type this text before the confirm button is enabled. */
    expectText?: string
    expectLabel?: string
    /** Extra condition from the parent (for example a filled password field). */
    canConfirm?: boolean
  }>(),
  // An absent boolean prop is cast to false, so the default has to be spelled out.
  { canConfirm: true },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()

const dialog = ref<HTMLDialogElement | null>(null)
const typed = ref('')
const titleId = useId()

const typedOk = computed(() => !props.expectText || typed.value === props.expectText)
const enabled = computed(() => typedOk.value && props.canConfirm && !props.busy)

watch(
  () => props.open,
  async (isOpen) => {
    const el = dialog.value
    if (!el) return
    if (isOpen && !el.open) {
      typed.value = ''
      el.showModal()
      await nextTick()
      el.querySelector<HTMLElement>('input, select, textarea')?.focus()
    } else if (!isOpen && el.open) {
      el.close()
    }
  },
  { flush: 'post' },
)

function onCancel(e: Event): void {
  e.preventDefault()
  if (!props.busy) emit('cancel')
}

function submit(): void {
  if (enabled.value) emit('confirm')
}
</script>

<template>
  <dialog ref="dialog" class="dialog" :aria-labelledby="titleId" @cancel="onCancel">
    <form class="dialog-body" @submit.prevent="submit">
      <h2 :id="titleId" class="dialog-title">{{ title }}</h2>
      <p v-if="message" class="dialog-message">{{ message }}</p>
      <FormField v-if="expectText" :label="expectLabel ?? ''">
        <template #default="{ id }">
          <input :id="id" v-model="typed" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" />
        </template>
      </FormField>
      <slot />
      <ErrorNote :message="error" />
      <div class="dialog-actions">
        <button type="button" class="btn" :disabled="busy" @click="emit('cancel')">
          {{ $t('common.cancel') }}
        </button>
        <button type="submit" class="btn" :class="danger ? 'btn-danger' : 'btn-primary'" :disabled="!enabled">
          {{ busy ? $t('common.working') : (confirmLabel ?? $t('common.confirm')) }}
        </button>
      </div>
    </form>
  </dialog>
</template>
