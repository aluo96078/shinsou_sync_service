<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { ApiError } from '@/api/client'
import { listCategories, listLibrary, type LibraryQuery } from '@/api/endpoints'
import type { Category, LibraryItem } from '@/api/types'
import CoverImage from '@/components/CoverImage.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import UnverifiedNotice from '@/components/UnverifiedNotice.vue'
import { errorMessage, formatNumber, formatPercent, t } from '@/i18n'

const PAGE_SIZE = 48
type Sort = NonNullable<LibraryQuery['sort']>
const SORTS: readonly Sort[] = ['recent', 'title', 'added']

const route = useRoute()
const router = useRouter()

function first(v: unknown): string {
  const x = Array.isArray(v) ? v[0] : v
  return typeof x === 'string' ? x : ''
}

const q = computed(() => first(route.query.q))
const category = computed(() => first(route.query.category))
const sort = computed<Sort>(() => {
  const s = first(route.query.sort)
  return (SORTS as readonly string[]).includes(s) ? (s as Sort) : 'recent'
})
const showArchived = computed(() => first(route.query.archived) === 'include')
const filtersActive = computed(() => q.value !== '' || category.value !== '' || showArchived.value)

const searchText = ref(q.value)
const items = ref<LibraryItem[]>([])
const total = ref(0)
const loading = ref(true)
const loadingMore = ref(false)
const error = ref<string | null>(null)
const unverified = ref(false)
const categories = ref<Category[]>([])

let seq = 0

function setQuery(patch: Record<string, string | undefined>): void {
  const next: LocationQueryRaw = { ...route.query, ...patch }
  for (const k of Object.keys(next)) if (next[k] === undefined || next[k] === '') delete next[k]
  void router.replace({ query: next })
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
watch(searchText, (v) => {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => {
    if (v.trim() !== q.value) setQuery({ q: v.trim() })
  }, 300)
})
// Keep the box in step with back/forward navigation.
watch(q, (v) => {
  if (v !== searchText.value.trim()) searchText.value = v
})
onBeforeUnmount(() => {
  if (debounceTimer) clearTimeout(debounceTimer)
})

function currentQuery(offset: number): LibraryQuery {
  const query: LibraryQuery = { sort: sort.value, offset, limit: PAGE_SIZE }
  if (q.value) query.q = q.value
  if (category.value) query.category = category.value
  if (showArchived.value) query.archived = 'include'
  return query
}

async function load(append: boolean): Promise<void> {
  const mine = ++seq
  if (append) loadingMore.value = true
  else loading.value = true
  error.value = null
  try {
    const page = await listLibrary(currentQuery(append ? items.value.length : 0))
    if (mine !== seq) return
    items.value = append ? [...items.value, ...page.items] : page.items
    total.value = page.total
    unverified.value = false
  } catch (e) {
    if (mine !== seq) return
    if (e instanceof ApiError && e.code === 'email_unverified') {
      unverified.value = true
      items.value = []
      total.value = 0
    } else {
      error.value = errorMessage(e)
    }
  } finally {
    if (mine === seq) {
      loading.value = false
      loadingMore.value = false
    }
  }
}

async function loadCategories(): Promise<void> {
  try {
    categories.value = (await listCategories()).categories.filter((c) => c.key !== 'default')
  } catch {
    categories.value = []
  }
}

watch([q, category, sort, showArchived], () => void load(false))
onMounted(() => {
  void load(false)
  void loadCategories()
})

function clearFilters(): void {
  searchText.value = ''
  void router.replace({ query: sort.value === 'recent' ? {} : { sort: sort.value } })
}

function ratio(item: LibraryItem): number | null {
  const p = item.progression
  return typeof p === 'number' && Number.isFinite(p) ? Math.min(1, Math.max(0, p)) : null
}
function author(item: LibraryItem): string {
  return item.author ?? ''
}
</script>

<template>
  <div>
    <div class="page-head">
      <h1>{{ $t('library.title') }}</h1>
      <span v-if="!unverified && !loading && !error" class="muted" aria-live="polite">
        {{ $t('library.total', { total: formatNumber(total) }) }}
      </span>
    </div>

    <UnverifiedNotice v-if="unverified" />

    <template v-else>
      <form class="filters" role="search" @submit.prevent>
        <div class="field">
          <label for="lib-q">{{ $t('library.search') }}</label>
          <input id="lib-q" v-model="searchText" type="search" autocomplete="off" maxlength="200" />
        </div>
        <div class="field">
          <label for="lib-cat">{{ $t('library.category') }}</label>
          <select id="lib-cat" :value="category" @change="setQuery({ category: ($event.target as HTMLSelectElement).value })">
            <option value="">{{ $t('library.categoryAll') }}</option>
            <option value="default">{{ $t('library.categoryDefault') }}</option>
            <option v-for="c in categories" :key="c.key" :value="c.key">{{ c.name || c.key }}</option>
          </select>
        </div>
        <div class="field">
          <label for="lib-sort">{{ $t('library.sort') }}</label>
          <select id="lib-sort" :value="sort" @change="setQuery({ sort: ($event.target as HTMLSelectElement).value === 'recent' ? undefined : ($event.target as HTMLSelectElement).value })">
            <option value="recent">{{ $t('library.sortRecent') }}</option>
            <option value="title">{{ $t('library.sortTitle') }}</option>
            <option value="added">{{ $t('library.sortAdded') }}</option>
          </select>
        </div>
        <label class="check">
          <input type="checkbox" :checked="showArchived" @change="setQuery({ archived: ($event.target as HTMLInputElement).checked ? 'include' : undefined })" />
          <span>{{ $t('library.showArchived') }}</span>
        </label>
      </form>

      <ErrorNote :message="error" />
      <p v-if="error" class="form-actions">
        <button type="button" class="btn" @click="load(false)">{{ $t('common.retry') }}</button>
      </p>

      <p v-if="loading" class="muted" role="status">{{ $t('app.loading') }}</p>

      <section v-else-if="!error && items.length === 0" class="empty-state">
        <template v-if="filtersActive">
          <h2>{{ $t('library.emptyFiltered') }}</h2>
          <button type="button" class="btn" @click="clearFilters">{{ $t('library.clearFilters') }}</button>
        </template>
        <template v-else>
          <h2>{{ $t('library.empty') }}</h2>
          <p>{{ $t('library.emptyBody') }}</p>
        </template>
      </section>

      <ul v-else-if="items.length > 0" class="book-grid">
        <li v-for="item in items" :key="item.key">
          <RouterLink class="book-card" :to="{ name: 'library-detail', params: { key: item.key } }">
            <div class="cover-wrap">
              <CoverImage :src="item.coverUrl" :title="item.title" />
              <span v-if="item.pinned || item.archived" class="badge-stack">
                <span v-if="item.pinned" class="badge badge-accent">{{ $t('library.pinned') }}</span>
                <span v-if="item.archived" class="badge">{{ $t('library.archived') }}</span>
              </span>
            </div>
            <p class="book-title">{{ item.title || $t('common.unknown') }}</p>
            <p v-if="author(item)" class="book-meta">{{ author(item) }}</p>
            <div
              v-if="ratio(item) !== null"
              class="progress"
              role="progressbar"
              :aria-label="$t('library.progress', { percent: formatPercent(ratio(item) ?? 0) })"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="Math.round((ratio(item) ?? 0) * 100)"
            >
              <span :style="{ width: `${(ratio(item) ?? 0) * 100}%` }"></span>
            </div>
            <p v-if="item.trackedUnits > 0" class="book-meta">
              {{ t('library.chapters', { read: item.readUnits, tracked: item.trackedUnits }) }}
            </p>
          </RouterLink>
        </li>
      </ul>

      <div v-if="!loading && items.length < total" class="load-more">
        <button type="button" class="btn" :disabled="loadingMore" @click="load(true)">
          {{ loadingMore ? $t('app.loading') : $t('common.loadMore') }}
        </button>
      </div>
    </template>
  </div>
</template>
