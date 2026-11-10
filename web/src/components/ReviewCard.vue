<script setup lang="ts">
import { ref, watch } from 'vue'
import { createReply, deleteReply, listReplies, markHelpful, unmarkHelpful } from '@/api/endpoints'
import type { HelpfulVote, Reply, Review } from '@/api/types'
import { errorMessage, formatDate, formatNumber } from '@/i18n'
import { MAX_REPLY_CHARS } from '@/lib/reviews'
import { useAction } from '@/lib/useAction'
import ConfirmDialog from './ConfirmDialog.vue'
import ErrorNote from './ErrorNote.vue'
import ReviewBody from './ReviewBody.vue'
import StarRating from './StarRating.vue'

const REPLY_PAGE = 50

const props = defineProps<{ review: Review; signedIn: boolean }>()
const emit = defineEmits<{
  vote: [vote: HelpfulVote]
  report: [target: { kind: 'review' | 'reply'; id: string }]
}>()

// --- helpful ----------------------------------------------------------------------------------
const voteAction = useAction()
async function toggleHelpful(): Promise<void> {
  const r = props.review
  const vote = await voteAction.run(() => (r.votedHelpful ? unmarkHelpful(r.id) : markHelpful(r.id)))
  if (vote) emit('vote', vote)
}

// --- replies (loaded when first expanded) -----------------------------------------------------
const open = ref(false)
const loaded = ref(false)
const loading = ref(false)
const loadError = ref<string | null>(null)
const replies = ref<Reply[]>([])
const total = ref(props.review.replyCount)

watch(
  () => props.review.replyCount,
  (n) => {
    if (!loaded.value) total.value = n
  },
)

let seq = 0
async function loadReplies(more: boolean): Promise<void> {
  const mine = ++seq
  loading.value = true
  loadError.value = null
  try {
    const res = await listReplies(props.review.id, {
      offset: more ? replies.value.length : 0,
      limit: REPLY_PAGE,
    })
    if (mine !== seq) return
    replies.value = more ? [...replies.value, ...res.replies] : res.replies
    total.value = res.total
    loaded.value = true
  } catch (e) {
    if (mine === seq) loadError.value = errorMessage(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

function toggleReplies(): void {
  open.value = !open.value
  if (open.value && !loaded.value) void loadReplies(false)
}

// --- writing and deleting replies -------------------------------------------------------------
const draft = ref('')
const postAction = useAction()
async function sendReply(): Promise<void> {
  const body = draft.value.trim()
  if (body === '') return
  const reply = await postAction.run(() => createReply(props.review.id, body))
  if (!reply) return
  draft.value = ''
  replies.value.push(reply)
  total.value += 1
}

const deleting = ref<Reply | null>(null)
const deleteAction = useAction()
function startDelete(reply: Reply): void {
  deleting.value = reply
  deleteAction.reset()
}
async function confirmDelete(): Promise<void> {
  const reply = deleting.value
  if (!reply) return
  const ok = await deleteAction.run(async () => {
    await deleteReply(reply.id)
    return true
  })
  if (!ok) return
  deleting.value = null
  replies.value = replies.value.filter((r) => r.id !== reply.id)
  total.value = Math.max(0, total.value - 1)
}
</script>

<template>
  <article class="review" :class="{ 'review-mine': review.mine }">
    <header class="review-head">
      <strong>@{{ review.author.username }}</strong>
      <StarRating :value="review.rating" />
      <span v-if="review.mine" class="badge badge-accent">{{ $t('reviews.mine') }}</span>
      <span v-if="review.status === 'hidden'" class="badge badge-warn">{{ $t('reviews.hidden') }}</span>
      <span class="muted">{{ formatDate(review.createdAt) }}</span>
      <span v-if="review.edited" class="badge">{{ $t('reviews.edited') }}</span>
    </header>
    <p v-if="review.status === 'hidden' && review.hiddenReason" class="hint">
      {{ $t('review.hiddenNotice', { reason: review.hiddenReason }) }}
    </p>

    <ReviewBody :body-html="review.bodyHtml" :spoiler="review.spoiler" />

    <div class="review-actions">
      <template v-if="signedIn">
        <button
          type="button"
          class="btn btn-small"
          :aria-pressed="review.votedHelpful"
          :class="{ 'btn-primary': review.votedHelpful }"
          :disabled="review.mine || voteAction.busy.value"
          :title="review.mine ? $t('reviews.helpfulOwn') : undefined"
          @click="toggleHelpful"
        >
          {{ $t('reviews.helpful', { n: formatNumber(review.helpful) }) }}
        </button>
      </template>
      <span v-else-if="review.helpful > 0" class="muted">{{ $t('reviews.helpfulCount', { n: formatNumber(review.helpful) }) }}</span>
      <button type="button" class="btn btn-small btn-ghost" :aria-expanded="open" @click="toggleReplies">
        {{ open ? $t('reviews.hideReplies') : $t('reviews.replies', { n: formatNumber(total) }) }}
      </button>
      <button
        v-if="signedIn && !review.mine"
        type="button"
        class="btn btn-small btn-ghost"
        @click="emit('report', { kind: 'review', id: review.id })"
      >
        {{ $t('reviews.report') }}
      </button>
    </div>
    <ErrorNote :message="voteAction.error.value" />

    <section v-if="open" class="replies">
      <ErrorNote :message="loadError" />
      <p v-if="loading && !loaded" class="muted" role="status">{{ $t('app.loading') }}</p>
      <template v-else-if="loaded">
        <p v-if="replies.length === 0" class="muted">{{ $t('reviews.noReplies') }}</p>
        <ul v-else class="reply-list">
          <li v-for="reply in replies" :key="reply.id" class="reply">
            <div class="review-head">
              <strong>@{{ reply.author.username }}</strong>
              <span v-if="reply.mine" class="badge badge-accent">{{ $t('reviews.mine') }}</span>
              <span v-if="reply.status === 'hidden'" class="badge badge-warn">{{ $t('reviews.hidden') }}</span>
              <span class="muted">{{ formatDate(reply.createdAt) }}</span>
            </div>
            <p v-if="reply.status === 'hidden' && reply.hiddenReason" class="hint">
              {{ $t('reviews.hiddenReply', { reason: reply.hiddenReason }) }}
            </p>
            <ReviewBody :body-html="reply.bodyHtml" />
            <div v-if="signedIn" class="review-actions">
              <button v-if="reply.mine" type="button" class="btn btn-small btn-link-danger" @click="startDelete(reply)">
                {{ $t('review.delete') }}
              </button>
              <button v-else type="button" class="btn btn-small btn-ghost" @click="emit('report', { kind: 'reply', id: reply.id })">
                {{ $t('reviews.report') }}
              </button>
            </div>
          </li>
        </ul>
        <button v-if="replies.length < total" type="button" class="btn btn-small" :disabled="loading" @click="loadReplies(true)">
          {{ $t('reviews.moreReplies') }}
        </button>
        <form v-if="signedIn" class="reply-form" @submit.prevent="sendReply">
          <div class="field">
            <label :for="`reply-${review.id}`">{{ $t('reviews.replyLabel') }}</label>
            <textarea :id="`reply-${review.id}`" v-model="draft" :maxlength="MAX_REPLY_CHARS" rows="3"></textarea>
          </div>
          <ErrorNote :message="postAction.error.value" />
          <button type="submit" class="btn btn-small btn-primary" :disabled="postAction.busy.value || draft.trim() === ''">
            {{ postAction.busy.value ? $t('common.working') : $t('reviews.replySend') }}
          </button>
        </form>
      </template>
    </section>

    <ConfirmDialog
      :open="deleting !== null"
      :title="$t('reviews.replyDeleteTitle')"
      :message="$t('reviews.replyDeleteBody')"
      :confirm-label="$t('review.delete')"
      danger
      :busy="deleteAction.busy.value"
      :error="deleteAction.error.value"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </article>
</template>
