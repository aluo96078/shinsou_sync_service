<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import {
  deleteReview,
  getWork,
  listWorkReviews,
  putReview,
  reportReply,
  reportReview,
  type ReviewSort,
} from '@/api/endpoints'
import { session } from '@/api/session'
import type { HelpfulVote, ReportInput, ReportReason, Review, ReviewInput, ReviewPage, WorkPage } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CoverImage from '@/components/CoverImage.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import Pager from '@/components/Pager.vue'
import ReportDialog from '@/components/ReportDialog.vue'
import ReviewCard from '@/components/ReviewCard.vue'
import ReviewsGate from '@/components/ReviewsGate.vue'
import StarPicker from '@/components/StarPicker.vue'
import StarRating from '@/components/StarRating.vue'
import { errorMessage, formatDecimal, formatNumber, t } from '@/i18n'
import { MAX_REVIEW_CHARS, canSubmitReview, charCount, lastPageOffset } from '@/lib/reviews'
import { useAction } from '@/lib/useAction'
import { useGate } from '@/lib/useGate'
import { loginLocation } from '@/router'

const LIMIT = 20
const SORTS: readonly ReviewSort[] = ['newest', 'helpful']

const route = useRoute()
const router = useRouter()
const hash = computed(() => String(route.params.hash ?? ''))
const signedIn = computed(() => session.user !== null)
const signInTarget = computed(() => loginLocation(route.fullPath))

const page = ref<WorkPage | null>(null)
const list = ref<ReviewPage | null>(null)
const sort = ref<ReviewSort>('newest')
const offset = ref(0)
const loading = ref(true)
const listLoading = ref(false)
const listError = ref<string | null>(null)
const notFound = ref(false)
const gate = useGate()
const notice = ref<string | null>(null)

function loadFailed(e: unknown): void {
  if (e instanceof ApiError && e.status === 404) notFound.value = true
  else if (e instanceof ApiError && e.status === 401 && !signedIn.value) void router.replace(signInTarget.value)
  else gate.fail(e)
}

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  gate.reset()
  notFound.value = false
  notice.value = null
  editing.value = false
  try {
    const [p, l] = await Promise.all([
      getWork(hash.value),
      listWorkReviews(hash.value, { sort: sort.value, offset: offset.value, limit: LIMIT }),
    ])
    if (mine !== seq) return
    page.value = p
    list.value = l
  } catch (e) {
    if (mine !== seq) return
    page.value = null
    list.value = null
    loadFailed(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

let listSeq = 0
async function loadList(): Promise<void> {
  const mine = ++listSeq
  listLoading.value = true
  listError.value = null
  try {
    const res = await listWorkReviews(hash.value, { sort: sort.value, offset: offset.value, limit: LIMIT })
    if (mine !== listSeq) return
    // The last item of a page went away: step back instead of showing an empty page.
    if (res.reviews.length === 0 && res.total > 0 && offset.value > 0) {
      offset.value = lastPageOffset(res.total, LIMIT)
      return
    }
    list.value = res
  } catch (e) {
    if (mine === listSeq) listError.value = errorMessage(e)
  } finally {
    if (mine === listSeq) listLoading.value = false
  }
}

/** Reloads the summary and the list after a write, without blanking the page. */
let refreshSeq = 0
async function refresh(): Promise<void> {
  const mine = ++refreshSeq
  try {
    const [p, l] = await Promise.all([
      getWork(hash.value),
      listWorkReviews(hash.value, { sort: sort.value, offset: offset.value, limit: LIMIT }),
    ])
    if (mine !== refreshSeq) return
    if (l.reviews.length === 0 && l.total > 0 && offset.value > 0) {
      page.value = p
      offset.value = lastPageOffset(l.total, LIMIT)
      return
    }
    page.value = p
    list.value = l
  } catch (e) {
    if (mine === refreshSeq) listError.value = errorMessage(e)
  }
}

onMounted(load)
watch(hash, () => {
  offset.value = 0
  void load()
})
watch(offset, loadList)
function showSort(s: ReviewSort): void {
  if (sort.value === s) return
  sort.value = s
  if (offset.value === 0) void loadList()
  else offset.value = 0
}

// --- my review --------------------------------------------------------------------------------
const mine = computed(() => page.value?.myReview ?? null)
const editing = ref(false)
const form = reactive({ rating: null as number | null, body: '', spoiler: false })
const saveAction = useAction()
const canSave = computed(() => canSubmitReview(form.rating, form.body))
const bodyLength = computed(() => charCount(form.body))

function startEdit(): void {
  const m = mine.value
  form.rating = m?.rating ?? null
  form.body = m?.body ?? ''
  form.spoiler = m?.spoiler ?? false
  saveAction.reset()
  notice.value = null
  editing.value = true
}

async function save(): Promise<void> {
  if (!canSave.value) return
  const input: ReviewInput = { rating: form.rating, body: form.body, spoiler: form.spoiler }
  const saved = await saveAction.run(() => putReview(hash.value, input))
  if (!saved) return
  editing.value = false
  notice.value = t('review.saved')
  await refresh()
}

const deleting = ref(false)
const deleteAction = useAction()
function startDelete(): void {
  deleting.value = true
  deleteAction.reset()
}
async function confirmDelete(): Promise<void> {
  const ok = await deleteAction.run(async () => {
    await deleteReview(hash.value)
    return true
  })
  if (!ok) return
  deleting.value = false
  editing.value = false
  notice.value = t('review.deleted')
  await refresh()
}

// --- votes and reports ------------------------------------------------------------------------
function onVote(review: Review, vote: HelpfulVote): void {
  review.helpful = vote.helpful
  review.votedHelpful = vote.votedHelpful
}

const reporting = ref<{ kind: 'review' | 'reply'; id: string } | null>(null)
const reportAction = useAction()
function startReport(target: { kind: 'review' | 'reply'; id: string }): void {
  reporting.value = target
  reportAction.reset()
  notice.value = null
}
async function sendReport(input: { reason: ReportReason; note: string }): Promise<void> {
  const target = reporting.value
  if (!target) return
  const note = input.note.trim()
  const body: ReportInput = note ? { reason: input.reason, note } : { reason: input.reason }
  const res = await reportAction.run(() =>
    target.kind === 'review' ? reportReview(target.id, body) : reportReply(target.id, body),
  )
  if (!res) return
  reporting.value = null
  notice.value = t('report.sent')
}

const average = computed(() => {
  const a = page.value?.rating.average
  return typeof a === 'number' ? formatDecimal(a, 2) : null
})
</script>

<template>
  <div>
    <p v-if="loading" class="muted" role="status">{{ $t('app.loading') }}</p>
    <section v-else-if="notFound" class="empty-state">
      <p>{{ $t('work.notFound') }}</p>
      <RouterLink v-if="signedIn" class="btn" :to="{ name: 'library' }">{{ $t('common.backToLibrary') }}</RouterLink>
    </section>
    <ReviewsGate v-else :unverified="gate.unverified.value" :disabled="gate.disabled.value" :error="gate.error.value" @retry="load">
      <template v-if="page">
        <div class="detail">
          <div class="detail-cover">
            <CoverImage :src="page.work.coverUrl" :title="page.work.title" />
          </div>
          <div>
            <h1>{{ page.work.title }}</h1>
            <p v-if="page.work.author" class="muted">{{ page.work.author }}</p>
            <p v-if="page.work.sourceName" class="muted">{{ $t('work.source', { name: page.work.sourceName }) }}</p>
            <p class="work-rating">
              <template v-if="average !== null">
                <strong class="work-average">{{ average }}</strong>
                <StarRating :value="Math.round(page.rating.average ?? 0)" />
                <span class="muted">{{ $t('work.ratings', { n: formatNumber(page.rating.count) }) }}</span>
              </template>
              <span v-else class="muted">{{ $t('work.noRatings') }}</span>
              <span class="muted"> · {{ $t('work.reviewCount', { n: formatNumber(page.reviewCount) }) }}</span>
            </p>
            <p v-if="page.inLibrary">
              <RouterLink class="btn btn-small" :to="{ name: 'library-detail', params: { key: page.work.hash } }">
                {{ $t('work.openInLibrary') }}
              </RouterLink>
            </p>
          </div>
        </div>

        <ErrorNote :message="notice" kind="success" />

        <section v-if="signedIn" class="card" aria-labelledby="mine-title">
          <h2 id="mine-title">{{ $t('review.mineTitle') }}</h2>
          <template v-if="!editing">
            <template v-if="mine">
              <p class="row">
                <StarRating :value="mine.rating" />
                <span v-if="mine.status === 'hidden'" class="badge badge-warn">{{ $t('reviews.hidden') }}</span>
              </p>
              <ErrorNote
                v-if="mine.status === 'hidden'"
                :message="$t('review.hiddenNotice', { reason: mine.hiddenReason ?? '—' })"
                kind="info"
              />
              <div class="row">
                <button type="button" class="btn" @click="startEdit">{{ $t('review.edit') }}</button>
                <button type="button" class="btn btn-link-danger" @click="startDelete">{{ $t('review.delete') }}</button>
              </div>
            </template>
            <template v-else-if="page.inLibrary">
              <button type="button" class="btn btn-primary" @click="startEdit">{{ $t('review.write') }}</button>
            </template>
            <p v-else class="muted">{{ $t('review.needLibrary') }}</p>
          </template>

          <form v-else @submit.prevent="save">
            <StarPicker v-model="form.rating" />
            <div class="field">
              <label for="review-body">{{ $t('review.body') }}</label>
              <textarea id="review-body" v-model="form.body" rows="6" aria-describedby="review-hint review-count"></textarea>
              <p id="review-count" class="hint char-count" :class="{ 'text-error': bodyLength > MAX_REVIEW_CHARS }">
                {{ $t('review.counter', { n: formatNumber(bodyLength), max: formatNumber(MAX_REVIEW_CHARS) }) }}
              </p>
              <p id="review-hint" class="hint">{{ $t('review.markdownHint') }}</p>
            </div>
            <label class="check">
              <input v-model="form.spoiler" type="checkbox" />
              <span>{{ $t('review.spoilerCheck') }}</span>
            </label>
            <p v-if="!canSave && bodyLength <= MAX_REVIEW_CHARS" class="hint">{{ $t('review.needContent') }}</p>
            <ErrorNote :message="saveAction.error.value" />
            <div class="form-actions">
              <button type="submit" class="btn btn-primary" :disabled="saveAction.busy.value || !canSave">
                {{ saveAction.busy.value ? $t('common.saving') : $t('common.save') }}
              </button>
              <button type="button" class="btn" :disabled="saveAction.busy.value" @click="editing = false">
                {{ $t('common.cancel') }}
              </button>
            </div>
          </form>
        </section>
        <p v-else class="note note-info">
          {{ $t('work.signInPrompt') }}
          <RouterLink :to="signInTarget">{{ $t('nav.signIn') }}</RouterLink>
        </p>

        <section aria-labelledby="reviews-title">
          <div class="page-head">
            <h2 id="reviews-title">{{ $t('reviews.section') }}</h2>
            <div class="subnav" role="group" :aria-label="$t('reviews.sort')">
              <button v-for="s in SORTS" :key="s" type="button" :aria-pressed="sort === s" @click="showSort(s)">
                {{ $t(`reviews.sort.${s}`) }}
              </button>
            </div>
          </div>
          <ErrorNote :message="listError" />
          <p v-if="list && list.total === 0" class="muted">{{ $t('reviews.none') }}</p>
          <template v-else-if="list">
            <ReviewCard
              v-for="r in list.reviews"
              :key="r.id"
              :review="r"
              :signed-in="signedIn"
              @vote="onVote(r, $event)"
              @report="startReport"
            />
            <Pager v-if="list.total > LIMIT" v-model:offset="offset" :total="list.total" :limit="LIMIT" :busy="listLoading" />
          </template>
        </section>
      </template>
    </ReviewsGate>

    <ConfirmDialog
      :open="deleting"
      :title="$t('review.deleteTitle')"
      :message="$t('review.deleteBody')"
      :confirm-label="$t('review.delete')"
      danger
      :busy="deleteAction.busy.value"
      :error="deleteAction.error.value"
      @confirm="confirmDelete"
      @cancel="deleting = false"
    />
    <ReportDialog
      :open="reporting !== null"
      :title="reporting?.kind === 'reply' ? $t('report.titleReply') : $t('report.titleReview')"
      :busy="reportAction.busy.value"
      :error="reportAction.error.value"
      @confirm="sendReport"
      @cancel="reporting = null"
    />
  </div>
</template>
