<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { listMyReviews } from '@/api/endpoints'
import type { ReviewPage } from '@/api/types'
import CoverImage from '@/components/CoverImage.vue'
import Pager from '@/components/Pager.vue'
import ReviewBody from '@/components/ReviewBody.vue'
import ReviewsGate from '@/components/ReviewsGate.vue'
import StarRating from '@/components/StarRating.vue'
import { formatDate } from '@/i18n'
import { lastPageOffset } from '@/lib/reviews'
import { useGate } from '@/lib/useGate'

const LIMIT = 20

const page = ref<ReviewPage | null>(null)
const offset = ref(0)
const loading = ref(true)
const gate = useGate()

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  gate.reset()
  try {
    const res = await listMyReviews({ offset: offset.value, limit: LIMIT })
    if (mine !== seq) return
    if (res.reviews.length === 0 && res.total > 0 && offset.value > 0) {
      offset.value = lastPageOffset(res.total, LIMIT)
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

onMounted(load)
watch(offset, load)
</script>

<template>
  <div>
    <div class="page-head"><h1>{{ $t('reviews.title') }}</h1></div>
    <p v-if="loading && !page && !gate.unverified.value && !gate.disabled.value && !gate.error.value" class="muted" role="status">
      {{ $t('app.loading') }}
    </p>
    <ReviewsGate v-else :unverified="gate.unverified.value" :disabled="gate.disabled.value" :error="gate.error.value" @retry="load">
      <template v-if="page">
        <p class="muted">{{ $t('reviews.intro') }}</p>
        <p v-if="page.total === 0" class="muted">{{ $t('reviews.empty') }}</p>
        <article v-for="r in page.reviews" :key="r.id" class="card review-work">
          <div class="review-work-cover">
            <CoverImage v-if="r.work" :src="r.work.coverUrl" :title="r.work.title" />
          </div>
          <div>
            <h2 class="share-title">
              <RouterLink v-if="r.work" :to="{ name: 'work', params: { hash: r.work.hash } }">{{ r.work.title }}</RouterLink>
            </h2>
            <p class="review-head">
              <StarRating :value="r.rating" />
              <span v-if="r.status === 'hidden'" class="badge badge-warn">{{ $t('reviews.hidden') }}</span>
              <span v-if="r.edited" class="badge">{{ $t('reviews.edited') }}</span>
              <span class="muted">{{ $t('reviews.updated', { date: formatDate(r.updatedAt) }) }}</span>
            </p>
            <p v-if="r.status === 'hidden' && r.hiddenReason" class="hint">
              {{ $t('review.hiddenNotice', { reason: r.hiddenReason }) }}
            </p>
            <ReviewBody :body-html="r.bodyHtml" :spoiler="r.spoiler" />
          </div>
        </article>
        <Pager v-if="page.total > LIMIT" v-model:offset="offset" :total="page.total" :limit="LIMIT" :busy="loading" />
      </template>
    </ReviewsGate>
  </div>
</template>
