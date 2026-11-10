<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { createShare, listRelation, searchUsers } from '@/api/endpoints'
import type { UserSummary } from '@/api/types'
import ConfirmDialog from './ConfirmDialog.vue'
import ErrorNote from './ErrorNote.vue'
import FormField from './FormField.vue'
import { errorMessage } from '@/i18n'
import { useAction } from '@/lib/useAction'

const MESSAGE_MAX = 500

const props = defineProps<{ open: boolean; work: string; title: string }>()
const emit = defineEmits<{ close: []; shared: [username: string] }>()

const query = ref('')
const candidates = ref<UserSummary[]>([])
/** True while `candidates` holds the accounts the user follows rather than search results. */
const suggested = ref(true)
const loading = ref(false)
const loadError = ref<string | null>(null)
const picked = ref<UserSummary | null>(null)
const message = ref('')
const sendAction = useAction()

let seq = 0
let timer: ReturnType<typeof setTimeout> | undefined

async function loadCandidates(): Promise<void> {
  const q = query.value.trim().replace(/^@/, '')
  const mine = ++seq
  loading.value = true
  loadError.value = null
  try {
    const users = q === ''
      ? (await listRelation('following', { limit: 50 })).users
      : (await searchUsers(q, 20)).users
    if (mine !== seq) return
    candidates.value = users
    suggested.value = q === ''
  } catch (e) {
    if (mine !== seq) return
    candidates.value = []
    loadError.value = errorMessage(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

watch(
  () => props.open,
  (isOpen) => {
    clearTimeout(timer)
    if (!isOpen) {
      seq++
      return
    }
    query.value = ''
    picked.value = null
    message.value = ''
    candidates.value = []
    sendAction.reset()
    void loadCandidates()
  },
)

// Searching is rate limited per account, so wait for a pause in typing.
watch(query, () => {
  if (!props.open) return
  clearTimeout(timer)
  timer = setTimeout(() => void loadCandidates(), 350)
})

const listed = computed(() => {
  const p = picked.value
  if (!p || candidates.value.some((u) => u.id === p.id)) return candidates.value
  return [p, ...candidates.value]
})

async function send(): Promise<void> {
  const to = picked.value
  if (!to) return
  const text = message.value.trim()
  const share = await sendAction.run(() =>
    createShare(text ? { recipientId: to.id, work: props.work, message: text } : { recipientId: to.id, work: props.work }),
  )
  if (share) emit('shared', to.username)
}
</script>

<template>
  <ConfirmDialog
    :open="open"
    :title="$t('share.title')"
    :message="$t('share.body', { title: title })"
    :confirm-label="$t('share.send')"
    :busy="sendAction.busy.value"
    :error="sendAction.error.value"
    :can-confirm="picked !== null"
    @confirm="send"
    @cancel="emit('close')"
  >
    <FormField :label="$t('share.recipient')" :hint="$t('share.recipientHint')">
      <template #default="{ id, describedby }">
        <input :id="id" v-model="query" type="search" maxlength="33" autocomplete="off" autocapitalize="off" spellcheck="false" :aria-describedby="describedby" @keydown.enter.prevent />
      </template>
    </FormField>

    <ErrorNote :message="loadError" />
    <fieldset class="pick-list">
      <legend class="sr-only">{{ $t('share.recipient') }}</legend>
      <p v-if="loading && listed.length === 0" class="muted" role="status">{{ $t('app.loading') }}</p>
      <p v-else-if="listed.length === 0" class="muted">
        {{ suggested ? $t('share.noFollowing') : $t('common.noResults') }}
      </p>
      <label v-for="u in listed" :key="u.id" class="check">
        <input type="radio" name="share-recipient" :value="u.id" :checked="picked?.id === u.id" :disabled="!u.canShare" @change="picked = u" />
        <span>
          @{{ u.username }}
          <span v-if="u.blocked" class="badge badge-danger">{{ $t('people.isBlocked') }}</span>
          <span v-else-if="!u.canShare" class="badge">{{ $t('people.noShares') }}</span>
          <span v-else-if="u.followsYou" class="badge">{{ $t('people.followsYou') }}</span>
        </span>
      </label>
    </fieldset>

    <FormField :label="$t('share.message')" optional>
      <template #default="{ id }">
        <textarea :id="id" v-model="message" :maxlength="MESSAGE_MAX" rows="3"></textarea>
      </template>
    </FormField>
  </ConfirmDialog>
</template>
