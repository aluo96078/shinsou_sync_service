<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { acceptShare, deleteShare, dismissShare, listShares } from '@/api/endpoints'
import type { Share, SharePage, ShareStatus } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CoverImage from '@/components/CoverImage.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import Pager from '@/components/Pager.vue'
import SocialGate from '@/components/SocialGate.vue'
import SocialLayout from '@/components/SocialLayout.vue'
import { formatDateTime, t } from '@/i18n'
import { safeHttpUrl } from '@/lib/redirect'
import { useAction } from '@/lib/useAction'
import { useGate } from '@/lib/useGate'
import { refreshPendingShares } from '@/stores/shares'

const LIMIT = 20

const props = defineProps<{ box: 'inbox' | 'sent' }>()
const inbox = computed(() => props.box === 'inbox')

const page = ref<SharePage | null>(null)
const status = ref<ShareStatus | ''>('')
const offset = ref(0)
const loading = ref(true)
const gate = useGate()
const notice = ref<string | null>(null)

// The sender is never told about a dismissal, so that filter is only offered for the inbox.
const statuses = computed<ShareStatus[]>(() =>
  inbox.value ? ['pending', 'accepted', 'dismissed'] : ['pending', 'accepted'],
)

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  gate.reset()
  try {
    const res = await listShares({
      box: props.box,
      status: status.value || undefined,
      offset: offset.value,
      limit: LIMIT,
    })
    if (mine !== seq) return
    // The last item of a page was removed: step back instead of showing an empty page.
    if (res.shares.length === 0 && res.total > 0 && offset.value > 0) {
      offset.value = Math.max(0, Math.floor((res.total - 1) / LIMIT) * LIMIT)
      return
    }
    page.value = res
  } catch (e) {
    if (mine !== seq) return
    page.value = null
    gate.fail(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

onMounted(() => {
  void load()
  void refreshPendingShares()
})
watch(offset, load)
// A filter only one box offers (dismissed) must not carry over to the other box.
watch(
  () => props.box,
  () => {
    if (status.value && !statuses.value.includes(status.value)) status.value = ''
  },
  { flush: 'sync' },
)
watch([status, () => props.box], () => {
  notice.value = null
  if (offset.value === 0) void load()
  else offset.value = 0
})

// --- answering -----------------------------------------------------------------------------------
const action = useAction()
const busyId = ref<string | null>(null)

async function answer(share: Share, accept: boolean): Promise<void> {
  notice.value = null
  busyId.value = share.id
  const updated = await action.run(() => (accept ? acceptShare(share.id) : dismissShare(share.id)))
  busyId.value = null
  if (!updated) return
  notice.value = accept ? t('shares.accepted', { title: updated.work.title }) : t('shares.dismissed')
  await Promise.all([load(), refreshPendingShares()])
}

// --- deleting ------------------------------------------------------------------------------------
const deleting = ref<Share | null>(null)
const deleteAction = useAction()

function startDelete(share: Share): void {
  deleting.value = share
  deleteAction.reset()
}
async function confirmDelete(): Promise<void> {
  const share = deleting.value
  if (!share) return
  const ok = await deleteAction.run(async () => {
    await deleteShare(share.id)
    return true
  })
  if (!ok) return
  deleting.value = null
  notice.value = t('shares.deleted')
  await Promise.all([load(), refreshPendingShares()])
}

function statusClass(s: ShareStatus): string {
  return s === 'accepted' ? 'badge-ok' : s === 'pending' ? 'badge-accent' : ''
}
</script>

<template>
  <SocialLayout :title="$t('social.title')">
    <p class="muted">{{ inbox ? $t('shares.inboxIntro') : $t('shares.sentIntro') }}</p>

    <SocialGate :unverified="gate.unverified.value" :disabled="gate.disabled.value" :error="gate.error.value" @retry="load">
      <div class="toolbar-row">
        <div class="field" style="flex: 0 1 14rem">
          <label for="share-status">{{ $t('shares.status') }}</label>
          <select id="share-status" v-model="status">
            <option value="">{{ $t('common.all') }}</option>
            <option v-for="s in statuses" :key="s" :value="s">{{ $t(`shares.status.${s}`) }}</option>
          </select>
        </div>
      </div>

      <ErrorNote :message="action.error.value" />
      <ErrorNote :message="notice" kind="success" />

      <p v-if="loading && !page" class="muted" role="status">{{ $t('app.loading') }}</p>
      <template v-else-if="page">
        <section v-if="page.total === 0" class="empty-state">
          <p>{{ status ? $t('common.noResults') : inbox ? $t('shares.inboxEmpty') : $t('shares.sentEmpty') }}</p>
          <RouterLink v-if="!status && inbox" class="btn" :to="{ name: 'people' }">{{ $t('shares.findPeople') }}</RouterLink>
          <RouterLink v-else-if="!status" class="btn" :to="{ name: 'library' }">{{ $t('common.backToLibrary') }}</RouterLink>
        </section>

        <ul v-else class="share-list">
          <li v-for="s in page.shares" :key="s.id" class="share-card card">
            <div class="share-cover">
              <CoverImage :src="s.work.coverUrl" :title="s.work.title" />
            </div>
            <div class="share-body">
              <h2 class="share-title">{{ s.work.title }}</h2>
              <p v-if="s.work.author" class="muted">{{ s.work.author }}</p>
              <p class="row">
                <span class="badge" :class="statusClass(s.status)">{{ $t(`shares.status.${s.status}`) }}</span>
                <span class="badge">{{ $t(`kind.${s.work.contentKind}`) }}</span>
                <span v-if="s.inLibrary" class="badge badge-ok">{{ $t('shares.inLibrary') }}</span>
              </p>
              <p>
                <template v-if="inbox">
                  {{ $t('shares.from') }}
                  <RouterLink :to="{ name: 'people', query: { q: s.sender.username } }">@{{ s.sender.username }}</RouterLink>
                </template>
                <template v-else>
                  {{ $t('shares.to') }}
                  <RouterLink :to="{ name: 'people', query: { q: s.recipient.username } }">@{{ s.recipient.username }}</RouterLink>
                </template>
                <span class="muted"> · {{ formatDateTime(s.createdAt) }}</span>
              </p>
              <blockquote v-if="s.message" class="share-message">{{ s.message }}</blockquote>
              <p class="hint">
                {{ $t('shares.source', { name: s.work.sourceName || s.work.packageId || $t('common.unknown') }) }}
                <template v-if="safeHttpUrl(s.work.repositoryUrl)">
                  · {{ $t('shares.repository') }}
                  <span class="mono share-url">{{ safeHttpUrl(s.work.repositoryUrl) }}</span>
                </template>
              </p>
              <div class="cell-actions">
                <template v-if="inbox && s.status === 'pending'">
                  <button type="button" class="btn btn-small btn-primary" :disabled="action.busy.value" @click="answer(s, true)">
                    {{ busyId === s.id ? $t('common.working') : $t('shares.accept') }}
                  </button>
                  <button type="button" class="btn btn-small" :disabled="action.busy.value" @click="answer(s, false)">
                    {{ $t('shares.dismiss') }}
                  </button>
                </template>
                <button
                  v-else-if="inbox && s.status === 'dismissed'"
                  type="button"
                  class="btn btn-small"
                  :disabled="action.busy.value"
                  @click="answer(s, true)"
                >
                  {{ busyId === s.id ? $t('common.working') : $t('shares.accept') }}
                </button>
                <button type="button" class="btn btn-small btn-link-danger" @click="startDelete(s)">
                  {{ inbox ? $t('common.delete') : $t('shares.withdraw') }}
                </button>
              </div>
            </div>
          </li>
        </ul>

        <Pager v-if="page.total > LIMIT" v-model:offset="offset" :total="page.total" :limit="LIMIT" :busy="loading" />
        <p v-if="inbox && page.total > 0" class="hint">{{ $t('shares.acceptHint') }}</p>
      </template>
    </SocialGate>

    <ConfirmDialog
      :open="deleting !== null"
      :title="inbox ? $t('shares.deleteTitle') : $t('shares.withdrawTitle')"
      :message="inbox ? $t('shares.deleteBody') : $t('shares.withdrawBody')"
      :confirm-label="inbox ? $t('common.delete') : $t('shares.withdraw')"
      danger
      :busy="deleteAction.busy.value"
      :error="deleteAction.error.value"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </SocialLayout>
</template>
