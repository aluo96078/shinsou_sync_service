<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  modDeleteReply,
  modDeleteReview,
  modListReplies,
  modListReports,
  modListReviews,
  modResolveReport,
  modSuspendUser,
  modUnhideReply,
  modUnhideReview,
  modUnsuspendUser,
} from '@/api/endpoints'
import type { ModReport, ModReportPage, ReplyPage, ResolveAction, ReviewPage, ReviewParty } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import Pager from '@/components/Pager.vue'
import ReviewBody from '@/components/ReviewBody.vue'
import StarRating from '@/components/StarRating.vue'
import { errorMessage, formatDate, formatDateTime, formatNumber, t } from '@/i18n'
import { MAX_REASON_CHARS, lastPageOffset } from '@/lib/reviews'
import { useAction } from '@/lib/useAction'

const LIMIT = 20
type Tab = 'open' | 'resolved' | 'reviews' | 'replies'
const TABS: readonly Tab[] = ['open', 'resolved', 'reviews', 'replies']

const tab = ref<Tab>('open')
const offset = ref(0)
const reports = ref<ModReportPage | null>(null)
const reviews = ref<ReviewPage | null>(null)
const replies = ref<ReplyPage | null>(null)
const openCount = ref<number | null>(null)
const loading = ref(true)
const error = ref<string | null>(null)
const notice = ref<string | null>(null)

const total = computed(() => {
  if (tab.value === 'reviews') return reviews.value?.total ?? 0
  if (tab.value === 'replies') return replies.value?.total ?? 0
  return reports.value?.total ?? 0
})
const loaded = computed(() => {
  if (tab.value === 'reviews') return reviews.value !== null
  if (tab.value === 'replies') return replies.value !== null
  return reports.value !== null
})

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  error.value = null
  const query = { offset: offset.value, limit: LIMIT }
  try {
    let count = 0
    let shown = 0
    if (tab.value === 'reviews') {
      const res = await modListReviews({ status: 'hidden', ...query })
      if (mine !== seq) return
      count = res.total
      shown = res.reviews.length
      if (shown > 0 || offset.value === 0) reviews.value = res
    } else if (tab.value === 'replies') {
      const res = await modListReplies({ status: 'hidden', ...query })
      if (mine !== seq) return
      count = res.total
      shown = res.replies.length
      if (shown > 0 || offset.value === 0) replies.value = res
    } else {
      const res = await modListReports({ status: tab.value, ...query })
      if (mine !== seq) return
      openCount.value = res.open
      count = res.total
      shown = res.reports.length
      if (shown > 0 || offset.value === 0) reports.value = res
    }
    // The last item of a page was answered: step back instead of showing an empty page.
    if (shown === 0 && count > 0 && offset.value > 0) offset.value = lastPageOffset(count, LIMIT)
  } catch (e) {
    if (mine === seq) error.value = errorMessage(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

/** The count in the first tab stays right while another tab is open. */
async function loadOpenCount(): Promise<void> {
  try {
    openCount.value = (await modListReports({ status: 'open', offset: 0, limit: 1 })).open
  } catch {
    // The tab itself shows what is wrong.
  }
}

onMounted(() => {
  void load()
  if (tab.value !== 'open') void loadOpenCount()
})
watch(offset, load)

function showTab(next: Tab): void {
  if (tab.value === next) return
  tab.value = next
  reports.value = null
  reviews.value = null
  replies.value = null
  notice.value = null
  action.reset()
  if (offset.value === 0) void load()
  else offset.value = 0
}

// --- actions ----------------------------------------------------------------------------------
type Pending =
  | { kind: 'resolve'; report: ModReport; action: ResolveAction }
  | { kind: 'delete-review'; id: string }
  | { kind: 'delete-reply'; id: string }
  | { kind: 'suspend'; user: ReviewParty }
  | { kind: 'unsuspend'; user: ReviewParty }

const action = useAction()
const pending = ref<Pending | null>(null)
const reason = ref('')

function ask(p: Pending): void {
  pending.value = p
  reason.value = ''
  action.reset()
  notice.value = null
}

const dialog = computed(() => {
  const p = pending.value
  if (!p) return { title: '', message: '', label: '', danger: false, needsReason: false }
  switch (p.kind) {
    case 'resolve':
      return {
        title: t(`mod.confirm.${p.action}Title`),
        message: t(`mod.confirm.${p.action}Body`),
        label: t(`mod.${p.action}`),
        danger: p.action === 'delete',
        needsReason: false,
      }
    case 'delete-review':
    case 'delete-reply':
      return {
        title: t('mod.confirm.deleteHiddenTitle'),
        message: t('mod.confirm.deleteHiddenBody'),
        label: t('mod.delete'),
        danger: true,
        needsReason: false,
      }
    case 'suspend':
      return {
        title: t('mod.suspendTitle', { username: p.user.username }),
        message: t('mod.suspendBody'),
        label: t('mod.suspend'),
        danger: true,
        needsReason: true,
      }
    case 'unsuspend':
      return {
        title: t('mod.unsuspendTitle', { username: p.user.username }),
        message: t('mod.unsuspendBody'),
        label: t('mod.unsuspend'),
        danger: false,
        needsReason: false,
      }
  }
})

const canConfirm = computed(() => !dialog.value.needsReason || reason.value.trim() !== '')

async function confirm(): Promise<void> {
  const p = pending.value
  if (!p) return
  const done = await action.run(async (): Promise<string> => {
    switch (p.kind) {
      case 'resolve':
        await modResolveReport(p.report.id, p.action)
        return t('mod.done.resolved')
      case 'delete-review':
        await modDeleteReview(p.id)
        return t('mod.done.deleted')
      case 'delete-reply':
        await modDeleteReply(p.id)
        return t('mod.done.deleted')
      case 'suspend':
        await modSuspendUser(p.user.id, reason.value.trim())
        return t('mod.done.suspended', { username: p.user.username })
      case 'unsuspend':
        await modUnsuspendUser(p.user.id)
        return t('mod.done.restored', { username: p.user.username })
    }
  })
  if (done === undefined) return
  pending.value = null
  notice.value = done
  await Promise.all([load(), loadOpenCount()])
}

async function unhide(kind: 'review' | 'reply', id: string): Promise<void> {
  notice.value = null
  const ok = await action.run(async () => {
    if (kind === 'review') await modUnhideReview(id)
    else await modUnhideReply(id)
    return true
  })
  if (!ok) return
  notice.value = t('mod.done.unhidden')
  await load()
}

/** Who wrote the reported content. */
function reportedAuthor(r: ModReport): ReviewParty | null {
  return r.reply?.author ?? r.review?.author ?? null
}
</script>

<template>
  <div>
    <div class="page-head"><h1>{{ $t('mod.title') }}</h1></div>
    <p class="muted">{{ $t('mod.intro') }}</p>

    <div class="subnav" role="group" :aria-label="$t('mod.title')">
      <button v-for="k in TABS" :key="k" type="button" :aria-pressed="tab === k" @click="showTab(k)">
        {{ $t(`mod.tab.${k}`) }}
        <span v-if="k === 'open' && openCount !== null && openCount > 0" class="badge badge-accent">{{ formatNumber(openCount) }}</span>
      </button>
    </div>

    <ErrorNote :message="pending ? null : action.error.value" />
    <ErrorNote :message="notice" kind="success" />
    <template v-if="error">
      <ErrorNote :message="error" />
      <button type="button" class="btn" @click="load">{{ $t('common.retry') }}</button>
    </template>
    <p v-else-if="loading && !loaded" class="muted" role="status">{{ $t('app.loading') }}</p>

    <template v-else-if="loaded">
      <p v-if="total === 0" class="muted">{{ $t(`mod.empty.${tab}`) }}</p>

      <!-- reports -->
      <template v-if="(tab === 'open' || tab === 'resolved') && reports">
        <article v-for="r in reports.reports" :key="r.id" class="card mod-item">
          <header class="review-head">
            <span class="badge">{{ r.targetType === 'reply' ? $t('mod.targetReply') : $t('mod.targetReview') }}</span>
            <span class="badge badge-warn">{{ $t(`report.reason.${r.reason}`) }}</span>
            <strong>{{ $t('mod.reporter', { username: r.reporter.username }) }}</strong>
            <span class="muted">{{ formatDateTime(r.createdAt) }}</span>
          </header>
          <p v-if="r.note" class="hint">{{ $t('mod.note', { note: r.note }) }}</p>

          <div v-if="r.review" class="mod-target">
            <p v-if="r.review.work">
              <RouterLink :to="{ name: 'work', params: { hash: r.review.work.hash } }">{{ r.review.work.title }}</RouterLink>
            </p>
            <template v-if="r.targetType === 'reply' && r.reply">
              <p class="hint">{{ $t('mod.replyOn', { username: r.review.author.username }) }}</p>
              <p><strong>@{{ r.reply.author.username }}</strong></p>
              <ReviewBody :body-html="r.reply.bodyHtml" />
            </template>
            <template v-else>
              <p class="review-head">
                <strong>@{{ r.review.author.username }}</strong>
                <StarRating :value="r.review.rating" />
              </p>
              <ReviewBody :body-html="r.review.bodyHtml" />
            </template>
          </div>

          <div v-if="r.status === 'open'" class="row">
            <button type="button" class="btn btn-small" @click="ask({ kind: 'resolve', report: r, action: 'dismiss' })">{{ $t('mod.dismiss') }}</button>
            <button type="button" class="btn btn-small" @click="ask({ kind: 'resolve', report: r, action: 'hide' })">{{ $t('mod.hide') }}</button>
            <button type="button" class="btn btn-small btn-danger" @click="ask({ kind: 'resolve', report: r, action: 'delete' })">{{ $t('mod.delete') }}</button>
            <template v-if="reportedAuthor(r)">
              <button type="button" class="btn btn-small btn-link-danger" @click="ask({ kind: 'suspend', user: reportedAuthor(r)! })">{{ $t('mod.suspend') }}</button>
              <button type="button" class="btn btn-small btn-ghost" @click="ask({ kind: 'unsuspend', user: reportedAuthor(r)! })">{{ $t('mod.unsuspend') }}</button>
            </template>
          </div>
          <p v-else class="row">
            <span v-if="r.resolution" class="badge badge-ok">{{ $t(`mod.resolution.${r.resolution}`) }}</span>
            <span v-if="r.resolvedBy" class="muted">{{ $t('mod.resolvedBy', { username: r.resolvedBy.username, date: formatDate(r.resolvedAt) }) }}</span>
          </p>
        </article>
        <Pager v-if="reports.total > LIMIT" v-model:offset="offset" :total="reports.total" :limit="LIMIT" :busy="loading" />
      </template>

      <!-- hidden reviews -->
      <template v-else-if="tab === 'reviews' && reviews">
        <article v-for="r in reviews.reviews" :key="r.id" class="card mod-item">
          <header class="review-head">
            <strong>{{ $t('mod.author', { username: r.author.username }) }}</strong>
            <StarRating :value="r.rating" />
            <span class="muted">{{ formatDateTime(r.createdAt) }}</span>
          </header>
          <p v-if="r.work">
            <RouterLink :to="{ name: 'work', params: { hash: r.work.hash } }">{{ r.work.title }}</RouterLink>
          </p>
          <p v-if="r.hiddenReason" class="hint">{{ $t('mod.hiddenReason', { reason: r.hiddenReason }) }}</p>
          <ReviewBody :body-html="r.bodyHtml" />
          <div class="row">
            <button type="button" class="btn btn-small" :disabled="action.busy.value" @click="unhide('review', r.id)">{{ $t('mod.unhide') }}</button>
            <button type="button" class="btn btn-small btn-danger" @click="ask({ kind: 'delete-review', id: r.id })">{{ $t('mod.delete') }}</button>
            <button type="button" class="btn btn-small btn-link-danger" @click="ask({ kind: 'suspend', user: r.author })">{{ $t('mod.suspend') }}</button>
            <button type="button" class="btn btn-small btn-ghost" @click="ask({ kind: 'unsuspend', user: r.author })">{{ $t('mod.unsuspend') }}</button>
          </div>
        </article>
        <Pager v-if="reviews.total > LIMIT" v-model:offset="offset" :total="reviews.total" :limit="LIMIT" :busy="loading" />
      </template>

      <!-- hidden replies -->
      <template v-else-if="tab === 'replies' && replies">
        <article v-for="r in replies.replies" :key="r.id" class="card mod-item">
          <header class="review-head">
            <strong>{{ $t('mod.author', { username: r.author.username }) }}</strong>
            <span class="muted">{{ formatDateTime(r.createdAt) }}</span>
          </header>
          <p v-if="r.hiddenReason" class="hint">{{ $t('mod.hiddenReason', { reason: r.hiddenReason }) }}</p>
          <ReviewBody :body-html="r.bodyHtml" />
          <div class="row">
            <button type="button" class="btn btn-small" :disabled="action.busy.value" @click="unhide('reply', r.id)">{{ $t('mod.unhide') }}</button>
            <button type="button" class="btn btn-small btn-danger" @click="ask({ kind: 'delete-reply', id: r.id })">{{ $t('mod.delete') }}</button>
            <button type="button" class="btn btn-small btn-link-danger" @click="ask({ kind: 'suspend', user: r.author })">{{ $t('mod.suspend') }}</button>
            <button type="button" class="btn btn-small btn-ghost" @click="ask({ kind: 'unsuspend', user: r.author })">{{ $t('mod.unsuspend') }}</button>
          </div>
        </article>
        <Pager v-if="replies.total > LIMIT" v-model:offset="offset" :total="replies.total" :limit="LIMIT" :busy="loading" />
      </template>
    </template>

    <ConfirmDialog
      :open="pending !== null"
      :title="dialog.title"
      :message="dialog.message"
      :confirm-label="dialog.label"
      :danger="dialog.danger"
      :can-confirm="canConfirm"
      :busy="action.busy.value"
      :error="action.error.value"
      @confirm="confirm"
      @cancel="pending = null"
    >
      <FormField v-if="dialog.needsReason" :label="$t('mod.reason')" :hint="$t('mod.reasonHint')">
        <template #default="{ id, describedby }">
          <textarea :id="id" v-model="reason" :maxlength="MAX_REASON_CHARS" :aria-describedby="describedby" rows="3"></textarea>
        </template>
      </FormField>
    </ConfirmDialog>
  </div>
</template>
