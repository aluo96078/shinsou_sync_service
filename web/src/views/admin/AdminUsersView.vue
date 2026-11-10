<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  adminDeleteUser,
  adminListUsers,
  adminSignOutUser,
  adminUpdateUser,
} from '@/api/endpoints'
import { session } from '@/api/session'
import type { AdminUser, AdminUserPatch, Role } from '@/api/types'
import AdminLayout from '@/components/AdminLayout.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import Pager from '@/components/Pager.vue'
import { errorMessage, formatDate, formatDateTime, formatNumber, t } from '@/i18n'
import { useAction } from '@/lib/useAction'

const LIMIT = 50
const ROLES: readonly Role[] = ['user', 'moderator', 'admin']

const users = ref<AdminUser[]>([])
const total = ref(0)
const offset = ref(0)
const q = ref('')
const loading = ref(true)
const loadError = ref<string | null>(null)
const notice = ref<string | null>(null)

let seq = 0
async function load(): Promise<void> {
  const mine = ++seq
  loading.value = true
  loadError.value = null
  try {
    const page = await adminListUsers({ q: q.value.trim() || undefined, offset: offset.value, limit: LIMIT })
    if (mine !== seq) return
    users.value = page.items
    total.value = page.total
  } catch (e) {
    if (mine === seq) loadError.value = errorMessage(e)
  } finally {
    if (mine === seq) loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | null = null
watch(q, () => {
  if (timer) clearTimeout(timer)
  timer = setTimeout(() => {
    offset.value = 0
    void load()
  }, 300)
})
onBeforeUnmount(() => {
  if (timer) clearTimeout(timer)
})
onMounted(load)

function setOffset(n: number): void {
  offset.value = n
  void load()
}

function quotaLabel(u: AdminUser): string {
  if (u.recordQuota === null || u.recordQuota === undefined) return t('admin.users.quotaDefault')
  return u.recordQuota === 0 ? t('admin.users.quotaUnlimited') : formatNumber(u.recordQuota)
}

function replaceUser(updated: AdminUser): void {
  users.value = users.value.map((u) => (u.id === updated.id ? updated : u))
  notice.value = t('admin.users.updated', { username: updated.username })
}

// --- edit (role + quota) -----------------------------------------------------------------------
const editing = ref<AdminUser | null>(null)
const editRole = ref<Role>('user')
const editQuota = ref('')
const editAction = useAction()

function startEdit(u: AdminUser): void {
  editing.value = u
  editRole.value = u.role
  editQuota.value = u.recordQuota === null || u.recordQuota === undefined ? '' : String(u.recordQuota)
  editAction.reset()
}

const quotaValid = computed(() => editQuota.value.trim() === '' || /^\d+$/.test(editQuota.value.trim()))

async function confirmEdit(): Promise<void> {
  const u = editing.value
  if (!u) return
  const patch: AdminUserPatch = {}
  if (editRole.value !== u.role) patch.role = editRole.value
  const quota = editQuota.value.trim() === '' ? null : Number(editQuota.value.trim())
  if (quota !== (u.recordQuota ?? null)) patch.recordQuota = quota
  if (Object.keys(patch).length === 0) {
    editing.value = null
    return
  }
  const updated = await editAction.run(() => adminUpdateUser(u.id, patch))
  if (updated) {
    replaceUser(updated)
    editing.value = null
  }
}

// --- simple confirmed actions ------------------------------------------------------------------
type Kind = 'suspend' | 'reactivate' | 'verify' | 'signout' | 'delete'
const pending = ref<{ kind: Kind; user: AdminUser } | null>(null)
const pendingAction = useAction()

function ask(kind: Kind, user: AdminUser): void {
  pending.value = { kind, user }
  pendingAction.reset()
}

const dialog = computed(() => {
  const p = pending.value
  if (!p) return { title: '', message: '', label: '', danger: false }
  const username = p.user.username
  switch (p.kind) {
    case 'suspend':
      return { title: t('admin.users.suspendTitle', { username }), message: t('admin.users.suspendBody'), label: t('admin.users.suspend'), danger: true }
    case 'reactivate':
      return { title: t('admin.users.reactivateTitle', { username }), message: t('admin.users.reactivateBody'), label: t('admin.users.reactivate'), danger: false }
    case 'verify':
      return { title: t('admin.users.verifyEmailTitle', { username }), message: p.user.email ?? '', label: t('admin.users.verifyEmail'), danger: false }
    case 'signout':
      return { title: t('admin.users.signOutTitle', { username }), message: t('admin.users.signOutBody'), label: t('admin.users.signOut'), danger: true }
    case 'delete':
      return { title: t('admin.users.deleteTitle', { username }), message: t('admin.users.deleteBody'), label: t('admin.users.delete'), danger: true }
  }
})

async function confirmPending(): Promise<void> {
  const p = pending.value
  if (!p) return
  const { kind, user } = p
  const ok = await pendingAction.run(async () => {
    switch (kind) {
      case 'suspend':
        replaceUser(await adminUpdateUser(user.id, { status: 'suspended' }))
        break
      case 'reactivate':
        replaceUser(await adminUpdateUser(user.id, { status: 'active' }))
        break
      case 'verify':
        replaceUser(await adminUpdateUser(user.id, { emailVerified: true }))
        break
      case 'signout':
        await adminSignOutUser(user.id)
        notice.value = t('admin.users.signedOut', { username: user.username })
        break
      case 'delete':
        await adminDeleteUser(user.id)
        notice.value = t('admin.users.deleted', { username: user.username })
        break
    }
    return true
  })
  if (ok) {
    pending.value = null
    if (kind === 'delete') await load()
  }
}
</script>

<template>
  <AdminLayout :title="$t('admin.nav.users')">
    <div class="toolbar-row">
      <div class="field">
        <label for="user-q">{{ $t('admin.users.search') }}</label>
        <input id="user-q" v-model="q" type="search" autocomplete="off" maxlength="200" />
      </div>
    </div>

    <ErrorNote :message="loadError" />
    <ErrorNote :message="notice" kind="success" />
    <p v-if="loading && users.length === 0" class="muted" role="status">{{ $t('app.loading') }}</p>
    <p v-else-if="!loadError && users.length === 0" class="muted">{{ $t('common.noResults') }}</p>

    <div v-else-if="users.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">{{ $t('admin.users.col.user') }}</th>
            <th scope="col">{{ $t('admin.users.col.email') }}</th>
            <th scope="col">{{ $t('admin.users.col.role') }}</th>
            <th scope="col">{{ $t('admin.users.col.status') }}</th>
            <th scope="col" class="num">{{ $t('admin.users.col.devices') }}</th>
            <th scope="col" class="num">{{ $t('admin.users.col.records') }}</th>
            <th scope="col">{{ $t('admin.users.col.quota') }}</th>
            <th scope="col">{{ $t('admin.users.col.lastSeen') }}</th>
            <th scope="col">{{ $t('admin.users.col.created') }}</th>
            <th scope="col"><span class="sr-only">{{ $t('common.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in users" :key="u.id">
            <td>
              <strong>{{ u.username }}</strong>
              <span v-if="u.id === session.user?.id" class="badge badge-accent">{{ $t('admin.users.you') }}</span>
            </td>
            <td style="overflow-wrap: anywhere">
              <template v-if="u.email">
                {{ u.email }}
                <span class="badge" :class="u.emailVerified ? 'badge-ok' : 'badge-warn'">
                  {{ u.emailVerified ? $t('account.verified') : $t('account.unverified') }}
                </span>
              </template>
              <span v-else class="muted">—</span>
            </td>
            <td>{{ $t(`role.${u.role}`) }}</td>
            <td>
              <span class="badge" :class="u.status === 'active' ? 'badge-ok' : 'badge-danger'">{{ $t(`status.${u.status}`) }}</span>
            </td>
            <td class="num">{{ formatNumber(u.deviceCount) }}</td>
            <td class="num">{{ formatNumber(u.recordCount) }}</td>
            <td>{{ quotaLabel(u) }}</td>
            <td class="nowrap">{{ u.lastSeenAt ? formatDateTime(u.lastSeenAt) : $t('common.never') }}</td>
            <td class="nowrap">{{ formatDate(u.createdAt) }}</td>
            <td>
              <div class="cell-actions">
                <button type="button" class="btn btn-small" @click="startEdit(u)">{{ $t('admin.users.edit') }}</button>
                <button v-if="u.status === 'active'" type="button" class="btn btn-small" @click="ask('suspend', u)">{{ $t('admin.users.suspend') }}</button>
                <button v-else type="button" class="btn btn-small" @click="ask('reactivate', u)">{{ $t('admin.users.reactivate') }}</button>
                <button v-if="u.email && !u.emailVerified" type="button" class="btn btn-small" @click="ask('verify', u)">{{ $t('admin.users.verifyEmail') }}</button>
                <button type="button" class="btn btn-small" @click="ask('signout', u)">{{ $t('admin.users.signOut') }}</button>
                <button type="button" class="btn btn-small btn-link-danger" @click="ask('delete', u)">{{ $t('admin.users.delete') }}</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pager v-if="total > 0" :total="total" :offset="offset" :limit="LIMIT" :busy="loading" @update:offset="setOffset" />

    <ConfirmDialog
      :open="editing !== null"
      :title="$t('admin.users.editTitle', { username: editing?.username ?? '' })"
      :confirm-label="$t('common.save')"
      :busy="editAction.busy.value"
      :error="editAction.error.value"
      :can-confirm="quotaValid"
      @confirm="confirmEdit"
      @cancel="editing = null"
    >
      <FormField :label="$t('admin.users.role')">
        <template #default="{ id }">
          <select :id="id" v-model="editRole">
            <option v-for="r in ROLES" :key="r" :value="r">{{ $t(`role.${r}`) }}</option>
          </select>
        </template>
      </FormField>
      <FormField :label="$t('admin.users.quota')" :hint="$t('admin.users.quotaHint')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="editQuota" type="text" inputmode="numeric" :aria-describedby="describedby" />
        </template>
      </FormField>
    </ConfirmDialog>

    <ConfirmDialog
      :open="pending !== null"
      :title="dialog.title"
      :message="dialog.message"
      :confirm-label="dialog.label"
      :danger="dialog.danger"
      :busy="pendingAction.busy.value"
      :error="pendingAction.error.value"
      :expect-text="pending?.kind === 'delete' ? pending.user.username : undefined"
      :expect-label="pending?.kind === 'delete' ? $t('admin.users.deleteType', { username: pending.user.username }) : undefined"
      @confirm="confirmPending"
      @cancel="pending = null"
    />
  </AdminLayout>
</template>
