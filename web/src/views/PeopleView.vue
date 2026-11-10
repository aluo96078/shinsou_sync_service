<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import {
  blockUser,
  followUser,
  getSharingPreferences,
  listRelation,
  searchUsers,
  setSharingPreferences,
  unblockUser,
  unfollowUser,
  type Relation,
} from '@/api/endpoints'
import type { ReceiveFrom, UserPage, UserSummary } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import Pager from '@/components/Pager.vue'
import SocialGate from '@/components/SocialGate.vue'
import SocialLayout from '@/components/SocialLayout.vue'
import { errorMessage, formatDate, formatNumber, t } from '@/i18n'
import { useAction } from '@/lib/useAction'
import { useGate } from '@/lib/useGate'
import { refreshPendingShares } from '@/stores/shares'

const LIMIT = 20
const RECEIVE: readonly ReceiveFrom[] = ['everyone', 'following', 'nobody']
const RELATIONS: readonly Relation[] = ['following', 'followers', 'blocks']

const route = useRoute()
const router = useRouter()
const gate = useGate()
const ready = ref(false)

// --- who may share with me -----------------------------------------------------------------------
const receiveFrom = ref<ReceiveFrom>('everyone')
const prefAction = useAction()
const prefSaved = ref(false)

async function savePreference(e: Event): Promise<void> {
  const next = (e.target as HTMLSelectElement).value as ReceiveFrom
  prefSaved.value = false
  const res = await prefAction.run(() => setSharingPreferences(next))
  if (res) {
    receiveFrom.value = res.receiveFrom
    prefSaved.value = true
  } else {
    // Put the control back on the stored value.
    ;(e.target as HTMLSelectElement).value = receiveFrom.value
  }
}

// --- search --------------------------------------------------------------------------------------
const query = ref('')
const results = ref<UserSummary[] | null>(null)
const searched = ref('')
const searching = ref(false)
const searchError = ref<string | null>(null)

let searchSeq = 0
async function runSearch(): Promise<void> {
  const q = query.value.trim().replace(/^@/, '')
  const mine = ++searchSeq
  searchError.value = null
  if (q === '') {
    results.value = null
    searched.value = ''
    return
  }
  searching.value = true
  try {
    const res = await searchUsers(q, 20)
    if (mine !== searchSeq) return
    results.value = res.users
    searched.value = q
  } catch (e) {
    if (mine !== searchSeq) return
    results.value = null
    searchError.value = errorMessage(e)
  } finally {
    if (mine === searchSeq) searching.value = false
  }
}

async function submitSearch(): Promise<void> {
  const q = query.value.trim().replace(/^@/, '')
  await router.replace({ name: 'people', query: q ? { q } : {} })
  await runSearch()
}

// --- lists ---------------------------------------------------------------------------------------
const relation = ref<Relation>('following')
const list = ref<UserPage | null>(null)
const offset = ref(0)
const listLoading = ref(false)
const listError = ref<string | null>(null)

let listSeq = 0
async function loadList(): Promise<void> {
  const mine = ++listSeq
  listLoading.value = true
  listError.value = null
  try {
    const res = await listRelation(relation.value, { offset: offset.value, limit: LIMIT })
    if (mine !== listSeq) return
    if (res.users.length === 0 && res.total > 0 && offset.value > 0) {
      offset.value = Math.max(0, Math.floor((res.total - 1) / LIMIT) * LIMIT)
      return
    }
    list.value = res
  } catch (e) {
    if (mine !== listSeq) return
    list.value = null
    listError.value = errorMessage(e)
  } finally {
    if (mine === listSeq) listLoading.value = false
  }
}

watch(offset, loadList)
function showRelation(r: Relation): void {
  if (relation.value === r) return
  relation.value = r
  list.value = null
  if (offset.value === 0) void loadList()
  else offset.value = 0
}

// --- first load ----------------------------------------------------------------------------------
async function load(): Promise<void> {
  gate.reset()
  ready.value = false
  try {
    receiveFrom.value = (await getSharingPreferences()).receiveFrom
  } catch (e) {
    gate.fail(e)
    return
  }
  ready.value = true
  const q = route.query.q
  query.value = typeof q === 'string' ? q : ''
  await Promise.all([loadList(), runSearch()])
}
onMounted(load)
watch(
  () => route.query.q,
  (q) => {
    const next = typeof q === 'string' ? q : ''
    if (ready.value && next !== searched.value) {
      query.value = next
      void runSearch()
    }
  },
)

// --- follow / block ------------------------------------------------------------------------------
const action = useAction()
const busyId = ref<string | null>(null)
const notice = ref<string | null>(null)

async function act(user: UserSummary, fn: (id: string) => Promise<void>, done: string): Promise<boolean> {
  notice.value = null
  busyId.value = user.id
  const ok = await action.run(async () => {
    await fn(user.id)
    return true
  })
  busyId.value = null
  if (!ok) return false
  notice.value = done
  await Promise.all([loadList(), runSearch()])
  return true
}

const follow = (u: UserSummary) => act(u, followUser, t('people.followed', { username: u.username }))
const unfollow = (u: UserSummary) => act(u, unfollowUser, t('people.unfollowed', { username: u.username }))
const unblock = (u: UserSummary) => act(u, unblockUser, t('people.unblocked', { username: u.username }))

const blocking = ref<UserSummary | null>(null)
function startBlock(u: UserSummary): void {
  blocking.value = u
  action.reset()
}
async function confirmBlock(): Promise<void> {
  const u = blocking.value
  if (!u) return
  const ok = await act(u, blockUser, t('people.blocked', { username: u.username }))
  if (!ok) return
  blocking.value = null
  // Blocking dismisses that person's open shares.
  await refreshPendingShares()
}

const emptyList = computed(() => t(`people.empty.${relation.value}`))
</script>

<template>
  <SocialLayout :title="$t('social.title')">
    <p v-if="!ready && !gate.unverified.value && !gate.disabled.value && !gate.error.value" class="muted" role="status">
      {{ $t('app.loading') }}
    </p>
    <SocialGate v-else :unverified="gate.unverified.value" :disabled="gate.disabled.value" :error="gate.error.value" @retry="load">
      <section class="card" aria-labelledby="receive-title">
        <h2 id="receive-title">{{ $t('people.receiveTitle') }}</h2>
        <div class="field">
          <label for="receive-from">{{ $t('people.receiveFrom') }}</label>
          <select id="receive-from" :value="receiveFrom" :disabled="prefAction.busy.value" aria-describedby="receive-hint" @change="savePreference">
            <option v-for="r in RECEIVE" :key="r" :value="r">{{ $t(`people.receive.${r}`) }}</option>
          </select>
          <p id="receive-hint" class="hint">{{ $t('people.receiveHint') }}</p>
        </div>
        <ErrorNote :message="prefAction.error.value" />
        <ErrorNote :message="prefSaved ? $t('people.receiveSaved') : null" kind="success" />
      </section>

      <ErrorNote :message="blocking ? null : action.error.value" />
      <ErrorNote :message="notice" kind="success" />

      <section class="card" aria-labelledby="search-title">
        <h2 id="search-title">{{ $t('people.searchTitle') }}</h2>
        <form class="toolbar-row" role="search" @submit.prevent="submitSearch">
          <div class="field">
            <label for="people-q">{{ $t('people.search') }}</label>
            <input id="people-q" v-model="query" type="search" maxlength="33" autocomplete="off" autocapitalize="off" spellcheck="false" />
          </div>
          <button type="submit" class="btn" :disabled="searching">{{ $t('common.search') }}</button>
        </form>
        <p class="hint">{{ $t('people.searchHint') }}</p>
        <ErrorNote :message="searchError" />
        <template v-if="results">
          <p v-if="results.length === 0" class="muted">{{ $t('people.noMatch', { q: searched }) }}</p>
          <ul v-else class="user-list">
            <li v-for="u in results" :key="u.id" class="user-row">
              <div>
                <strong>@{{ u.username }}</strong>
                <span v-if="u.following" class="badge badge-accent">{{ $t('people.youFollow') }}</span>
                <span v-if="u.followsYou" class="badge">{{ $t('people.followsYou') }}</span>
                <span v-if="u.blocked" class="badge badge-danger">{{ $t('people.isBlocked') }}</span>
                <span v-else-if="!u.canShare" class="badge">{{ $t('people.noShares') }}</span>
              </div>
              <div class="cell-actions">
                <template v-if="u.blocked">
                  <button type="button" class="btn btn-small" :disabled="action.busy.value" @click="unblock(u)">{{ $t('people.unblock') }}</button>
                </template>
                <template v-else>
                  <button v-if="u.following" type="button" class="btn btn-small" :disabled="action.busy.value" @click="unfollow(u)">{{ $t('people.unfollow') }}</button>
                  <button v-else type="button" class="btn btn-small btn-primary" :disabled="action.busy.value" @click="follow(u)">
                    {{ busyId === u.id ? $t('common.working') : $t('people.follow') }}
                  </button>
                  <button type="button" class="btn btn-small btn-link-danger" :disabled="action.busy.value" @click="startBlock(u)">{{ $t('people.block') }}</button>
                </template>
              </div>
            </li>
          </ul>
        </template>
      </section>

      <section aria-labelledby="lists-title">
        <h2 id="lists-title" class="sr-only">{{ $t('people.listsTitle') }}</h2>
        <div class="subnav" role="group" :aria-label="$t('people.listsTitle')">
          <button v-for="r in RELATIONS" :key="r" type="button" :aria-pressed="relation === r" @click="showRelation(r)">
            {{ $t(`people.tab.${r}`) }}
          </button>
        </div>
        <ErrorNote :message="listError" />
        <p v-if="listLoading && !list" class="muted" role="status">{{ $t('app.loading') }}</p>
        <template v-else-if="list">
          <p v-if="list.total === 0" class="muted">{{ emptyList }}</p>
          <ul v-else class="user-list">
            <li v-for="u in list.users" :key="u.id" class="user-row">
              <div>
                <strong>@{{ u.username }}</strong>
                <span v-if="relation !== 'following' && u.following" class="badge badge-accent">{{ $t('people.youFollow') }}</span>
                <span v-if="relation !== 'followers' && u.followsYou" class="badge">{{ $t('people.followsYou') }}</span>
                <span v-if="u.since" class="muted"> · {{ $t('people.since', { date: formatDate(u.since) }) }}</span>
              </div>
              <div class="cell-actions">
                <template v-if="relation === 'blocks'">
                  <button type="button" class="btn btn-small" :disabled="action.busy.value" @click="unblock(u)">{{ $t('people.unblock') }}</button>
                </template>
                <template v-else>
                  <button v-if="u.following" type="button" class="btn btn-small" :disabled="action.busy.value" @click="unfollow(u)">{{ $t('people.unfollow') }}</button>
                  <button v-else type="button" class="btn btn-small btn-primary" :disabled="action.busy.value" @click="follow(u)">{{ $t('people.follow') }}</button>
                  <button type="button" class="btn btn-small btn-link-danger" :disabled="action.busy.value" @click="startBlock(u)">{{ $t('people.block') }}</button>
                </template>
              </div>
            </li>
          </ul>
          <Pager v-if="list.total > LIMIT" v-model:offset="offset" :total="list.total" :limit="LIMIT" :busy="listLoading" />
          <p v-else-if="list.total > 0" class="muted">{{ $t('people.count', { total: formatNumber(list.total) }) }}</p>
        </template>
      </section>
    </SocialGate>

    <ConfirmDialog
      :open="blocking !== null"
      :title="$t('people.blockTitle', { username: blocking?.username ?? '' })"
      :message="$t('people.blockBody')"
      :confirm-label="$t('people.block')"
      danger
      :busy="action.busy.value"
      :error="action.error.value"
      @confirm="confirmBlock"
      @cancel="blocking = null"
    />
  </SocialLayout>
</template>
