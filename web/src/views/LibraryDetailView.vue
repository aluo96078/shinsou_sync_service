<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ApiError } from '@/api/client'
import { getLibraryEntry, listCategories } from '@/api/endpoints'
import type { Category, LibraryDetail } from '@/api/types'
import CoverImage from '@/components/CoverImage.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import ShareDialog from '@/components/ShareDialog.vue'
import UnverifiedNotice from '@/components/UnverifiedNotice.vue'
import { reviewsFeature, sharingFeature } from '@/stores/instance'
import { errorMessage, formatDate, formatDateTime, formatNumber, formatPercent, t } from '@/i18n'

const CHUNK = 200

const route = useRoute()
const key = computed(() => {
  const k = route.params.key
  return Array.isArray(k) ? k.join('/') : (k ?? '')
})

const detail = ref<LibraryDetail | null>(null)
const categories = ref<Category[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const notFound = ref(false)
const unverified = ref(false)

const filterText = ref('')
const readFilter = ref<'all' | 'read' | 'unread'>('all')
const bookmarkedOnly = ref(false)
const visible = ref(CHUNK)

// Entries imported from local files have no work descriptor and cannot be shared.
const shareWork = computed(() => (sharingFeature.value ? (detail.value?.work?.hash ?? null) : null))
const sharing = ref(false)
const shareNotice = ref<string | null>(null)

function onShared(username: string): void {
  sharing.value = false
  shareNotice.value = t('share.sent', { username })
}

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  error.value = null
  notFound.value = false
  unverified.value = false
  detail.value = null
  sharing.value = false
  shareNotice.value = null
  try {
    const [d, c] = await Promise.all([
      getLibraryEntry(key.value),
      listCategories().catch(() => ({ categories: [] as Category[] })),
    ])
    if (mine !== seq) return
    detail.value = d
    categories.value = c.categories
  } catch (e) {
    if (mine !== seq) return
    if (e instanceof ApiError && e.code === 'not_found') notFound.value = true
    else if (e instanceof ApiError && e.code === 'email_unverified') unverified.value = true
    else error.value = errorMessage(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

onMounted(load)
watch(key, load)
watch([filterText, readFilter, bookmarkedOnly], () => {
  visible.value = CHUNK
})

const categoryNames = computed(() => {
  const d = detail.value
  if (!d) return []
  if (d.categoryIds.length === 0) return [t('library.categoryDefault')]
  const byKey = new Map(categories.value.map((c) => [c.key, c.name]))
  return d.categoryIds.map((id) => byKey.get(id) || (id === 'default' ? t('library.categoryDefault') : id))
})

const kindLabel = computed(() => {
  const k = detail.value?.contentKind
  return k ? t(`kind.${k}`) : t('kind.unknown')
})

const progression = computed(() => {
  const p = detail.value?.progression
  return typeof p === 'number' && Number.isFinite(p) ? Math.min(1, Math.max(0, p)) : null
})

const filtered = computed(() => {
  const units = detail.value?.units ?? []
  const needle = filterText.value.trim().toLowerCase()
  return units.filter((u) => {
    if (readFilter.value === 'read' && !u.read) return false
    if (readFilter.value === 'unread' && u.read) return false
    if (bookmarkedOnly.value && !u.bookmarked) return false
    return needle === '' || u.unitRemoteId.toLowerCase().includes(needle)
  })
})
const shown = computed(() => filtered.value.slice(0, visible.value))

function yesNo(v: boolean): string {
  return v ? t('common.yes') : '—'
}
</script>

<template>
  <div>
    <p><RouterLink :to="{ name: 'library' }">← {{ $t('detail.back') }}</RouterLink></p>

    <p v-if="loading" class="muted" role="status">{{ $t('app.loading') }}</p>
    <UnverifiedNotice v-else-if="unverified" />
    <section v-else-if="notFound" class="empty-state">
      <p>{{ $t('detail.notFound') }}</p>
      <RouterLink class="btn" :to="{ name: 'library' }">{{ $t('common.backToLibrary') }}</RouterLink>
    </section>
    <template v-else-if="error">
      <ErrorNote :message="error" />
      <button type="button" class="btn" @click="load">{{ $t('common.retry') }}</button>
    </template>

    <template v-else-if="detail">
      <div class="detail">
        <div class="detail-cover">
          <CoverImage :src="detail.coverUrl" :title="detail.title" />
        </div>
        <div>
          <h1>{{ detail.title || $t('common.unknown') }}</h1>
          <p v-if="detail.author" class="muted">{{ detail.author }}</p>
          <p v-if="detail.pinned || detail.archived || detail.local" class="row">
            <span v-if="detail.pinned" class="badge badge-accent">{{ $t('library.pinned') }}</span>
            <span v-if="detail.archived" class="badge">{{ $t('library.archived') }}</span>
            <span v-if="detail.local" class="badge badge-warn">{{ $t('detail.local') }}</span>
          </p>
          <p v-if="detail.local" class="hint">{{ $t('detail.localHint') }}</p>
          <p v-if="shareWork">
            <button type="button" class="btn" @click="sharing = true; shareNotice = null">{{ $t('share.button') }}</button>
          </p>
          <p v-if="reviewsFeature && detail.work?.hash">
            <RouterLink class="btn" :to="{ name: 'work', params: { hash: detail.work.hash } }">{{ $t('detail.reviews') }}</RouterLink>
          </p>
          <ErrorNote :message="shareNotice" kind="success" />

          <div v-if="progression !== null" style="margin: 1rem 0">
            <p style="margin-bottom: 0.25rem">
              {{ $t('detail.progress') }}: <strong>{{ formatPercent(progression) }}</strong>
            </p>
            <div class="progress progress-lg" role="progressbar" :aria-label="$t('detail.progress')" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="Math.round(progression * 100)">
              <span :style="{ width: `${progression * 100}%` }"></span>
            </div>
          </div>

          <dl class="facts">
            <template v-if="!detail.local">
              <dt>{{ $t('detail.source') }}</dt>
              <dd>{{ detail.sourceName || '—' }}</dd>
            </template>
            <dt>{{ $t('detail.language') }}</dt>
            <dd>{{ detail.language || '—' }}</dd>
            <dt>{{ $t('detail.kind') }}</dt>
            <dd>{{ kindLabel }}</dd>
            <dt>{{ $t('detail.categories') }}</dt>
            <dd>{{ categoryNames.join('、') }}</dd>
            <dt>{{ $t('detail.addedAt') }}</dt>
            <dd>{{ formatDate(detail.addedAt) }}</dd>
            <dt>{{ $t('detail.lastReadAt') }}</dt>
            <dd>{{ formatDateTime(detail.lastReadAt) }}</dd>
            <dt>{{ $t('detail.currentChapter') }}</dt>
            <dd class="mono">{{ detail.currentUnitRemoteId || '—' }}</dd>
          </dl>
        </div>
      </div>

      <section aria-labelledby="chapters-title">
        <div class="page-head">
          <h2 id="chapters-title">{{ $t('detail.chapters') }}</h2>
          <span class="muted">{{ $t('library.chapters', { read: detail.readUnits, tracked: detail.trackedUnits }) }}</span>
        </div>

        <p v-if="detail.units.length === 0" class="muted">{{ $t('detail.noChapters') }}</p>
        <template v-else>
          <div class="toolbar-row">
            <div class="field">
              <label for="unit-filter">{{ $t('detail.filterChapters') }}</label>
              <input id="unit-filter" v-model="filterText" type="search" autocomplete="off" />
            </div>
            <div class="field" style="flex: 0 1 10rem">
              <label for="unit-read">{{ $t('detail.col.read') }}</label>
              <select id="unit-read" v-model="readFilter">
                <option value="all">{{ $t('detail.filterAll') }}</option>
                <option value="read">{{ $t('detail.filterRead') }}</option>
                <option value="unread">{{ $t('detail.filterUnread') }}</option>
              </select>
            </div>
            <label class="check" style="margin: 0">
              <input v-model="bookmarkedOnly" type="checkbox" />
              <span>{{ $t('detail.filterBookmarked') }}</span>
            </label>
          </div>

          <div class="table-wrap">
            <table class="data">
              <thead>
                <tr>
                  <th scope="col">{{ $t('detail.col.chapter') }}</th>
                  <th scope="col">{{ $t('detail.col.read') }}</th>
                  <th scope="col">{{ $t('detail.col.bookmarked') }}</th>
                  <th scope="col" class="num">{{ $t('detail.col.lastPage') }}</th>
                  <th scope="col">{{ $t('detail.col.updated') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="u in shown" :key="u.unitRemoteId">
                  <td class="mono" style="overflow-wrap: anywhere">
                    {{ u.unitRemoteId }}
                    <span v-if="u.unitRemoteId === detail.currentUnitRemoteId" class="badge badge-accent">{{ $t('detail.currentChapter') }}</span>
                  </td>
                  <td>{{ yesNo(u.read) }}</td>
                  <td>{{ yesNo(u.bookmarked) }}</td>
                  <td class="num">{{ typeof u.lastPageIndex === 'number' ? formatNumber(u.lastPageIndex + 1) : '—' }}</td>
                  <td class="nowrap">{{ formatDateTime(u.updatedAt) }}</td>
                </tr>
                <tr v-if="filtered.length === 0">
                  <td colspan="5" class="muted">{{ $t('common.noResults') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="pager">
            <span class="muted">{{ $t('detail.shown', { shown: shown.length, total: filtered.length }) }}</span>
            <button v-if="shown.length < filtered.length" type="button" class="btn btn-small" @click="visible += CHUNK">
              {{ $t('detail.showMore', { n: Math.min(CHUNK, filtered.length - shown.length) }) }}
            </button>
          </div>
        </template>
      </section>

      <ShareDialog
        v-if="shareWork"
        :open="sharing"
        :work="shareWork"
        :title="detail.title || $t('common.unknown')"
        @close="sharing = false"
        @shared="onShared"
      />
    </template>
  </div>
</template>
