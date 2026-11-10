<script setup lang="ts">
import { ref, watch } from 'vue'
import type { ReportReason } from '@/api/types'
import { MAX_NOTE_CHARS, REPORT_REASONS } from '@/lib/reviews'
import ConfirmDialog from './ConfirmDialog.vue'
import FormField from './FormField.vue'

const props = defineProps<{
  open: boolean
  title: string
  busy?: boolean
  error?: string | null
}>()
const emit = defineEmits<{ confirm: [input: { reason: ReportReason; note: string }]; cancel: [] }>()

const reason = ref<ReportReason>('spam')
const note = ref('')

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      reason.value = 'spam'
      note.value = ''
    }
  },
)
</script>

<template>
  <ConfirmDialog
    :open="open"
    :title="title"
    :message="$t('report.body')"
    :confirm-label="$t('report.send')"
    :busy="busy"
    :error="error"
    @confirm="emit('confirm', { reason, note })"
    @cancel="emit('cancel')"
  >
    <FormField :label="$t('report.reason')">
      <template #default="{ id }">
        <select :id="id" v-model="reason">
          <option v-for="r in REPORT_REASONS" :key="r" :value="r">{{ $t(`report.reason.${r}`) }}</option>
        </select>
      </template>
    </FormField>
    <FormField :label="$t('report.note')" optional>
      <template #default="{ id }">
        <textarea :id="id" v-model="note" :maxlength="MAX_NOTE_CHARS"></textarea>
      </template>
    </FormField>
  </ConfirmDialog>
</template>
