<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { adminCreateInvite, adminDeleteInvite, adminListInvites } from '@/api/endpoints'
import type { Invite } from '@/api/types'
import AdminLayout from '@/components/AdminLayout.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { errorMessage, formatDateTime, t } from '@/i18n'
import { useAction } from '@/lib/useAction'

const invites = ref<Invite[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    invites.value = (await adminListInvites()).invites
  } catch (e) {
    loadError.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// --- create ------------------------------------------------------------------------------------
const maxUses = ref(1)
const expiresIn = ref<number | null>(null)
const note = ref('')
const createAction = useAction()
const newCode = ref<string | null>(null)
const copied = ref(false)
const codeEl = ref<HTMLElement | null>(null)

async function create(): Promise<void> {
  copied.value = false
  const res = await createAction.run(() =>
    adminCreateInvite({
      maxUses: maxUses.value || 1,
      expiresInHours: expiresIn.value ? expiresIn.value : null,
      note: note.value.trim() || null,
    }),
  )
  if (res) {
    newCode.value = res.code
    note.value = ''
    await load()
  }
}

async function copy(): Promise<void> {
  if (!newCode.value) return
  try {
    await navigator.clipboard.writeText(newCode.value)
    copied.value = true
  } catch {
    // Clipboard API unavailable (or denied): select the code so Ctrl+C works.
    const el = codeEl.value
    if (el) {
      const range = document.createRange()
      range.selectNodeContents(el)
      const sel = window.getSelection()
      sel?.removeAllRanges()
      sel?.addRange(range)
    }
  }
}

// --- revoke ------------------------------------------------------------------------------------
const revoking = ref<Invite | null>(null)
const revokeAction = useAction()

async function confirmRevoke(): Promise<void> {
  const inv = revoking.value
  if (!inv) return
  const ok = await revokeAction.run(async () => {
    await adminDeleteInvite(inv.id)
    return true
  })
  if (ok) {
    revoking.value = null
    await load()
  }
}

function status(inv: Invite): 'expired' | 'exhausted' | null {
  if (inv.uses >= inv.maxUses) return 'exhausted'
  if (inv.expiresAt != null && inv.expiresAt <= Date.now()) return 'expired'
  return null
}
</script>

<template>
  <AdminLayout :title="$t('admin.nav.invites')">
    <section class="card" aria-labelledby="invite-create-title">
      <h2 id="invite-create-title">{{ $t('admin.invites.create') }}</h2>
      <form @submit.prevent="create">
        <div class="toolbar-row">
          <FormField :label="$t('admin.invites.maxUses')">
            <template #default="{ id }">
              <input :id="id" v-model.number="maxUses" type="number" min="1" max="1000" required />
            </template>
          </FormField>
          <FormField :label="$t('admin.invites.expiresIn')" :hint="$t('admin.invites.expiresHint')" optional>
            <template #default="{ id, describedby }">
              <input :id="id" v-model.number="expiresIn" type="number" min="1" max="8760" :aria-describedby="describedby" />
            </template>
          </FormField>
          <FormField :label="$t('admin.invites.note')" optional>
            <template #default="{ id }">
              <input :id="id" v-model="note" type="text" maxlength="200" />
            </template>
          </FormField>
        </div>
        <ErrorNote :message="createAction.error.value" />
        <button type="submit" class="btn btn-primary" :disabled="createAction.busy.value">{{ $t('admin.invites.submit') }}</button>
      </form>

      <div v-if="newCode" class="stack" style="margin-top: 1.25rem" role="status">
        <h3>{{ $t('admin.invites.codeTitle') }}</h3>
        <div class="code-box">
          <code ref="codeEl">{{ newCode }}</code>
          <button type="button" class="btn" @click="copy">{{ copied ? $t('common.copied') : $t('common.copy') }}</button>
        </div>
        <ErrorNote :message="$t('admin.invites.codeWarning')" kind="info" />
      </div>
    </section>

    <ErrorNote :message="loadError" />
    <p v-if="loading && invites.length === 0" class="muted" role="status">{{ $t('app.loading') }}</p>
    <p v-else-if="!loadError && invites.length === 0" class="muted">{{ $t('admin.invites.empty') }}</p>
    <div v-else-if="invites.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">{{ $t('admin.invites.col.note') }}</th>
            <th scope="col" class="num">{{ $t('admin.invites.col.uses') }}</th>
            <th scope="col">{{ $t('admin.invites.col.expires') }}</th>
            <th scope="col">{{ $t('admin.invites.col.creator') }}</th>
            <th scope="col">{{ $t('admin.invites.col.created') }}</th>
            <th scope="col"><span class="sr-only">{{ $t('common.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="inv in invites" :key="inv.id">
            <td>
              {{ inv.note || '—' }}
              <span v-if="status(inv)" class="badge badge-warn">
                {{ status(inv) === 'expired' ? $t('admin.invites.expired') : $t('admin.invites.exhausted') }}
              </span>
            </td>
            <td class="num">{{ inv.uses }} / {{ inv.maxUses }}</td>
            <td class="nowrap">{{ inv.expiresAt != null ? formatDateTime(inv.expiresAt) : t('admin.invites.noExpiry') }}</td>
            <td>{{ inv.createdBy ?? $t('admin.invites.cli') }}</td>
            <td class="nowrap">{{ formatDateTime(inv.createdAt) }}</td>
            <td>
              <button type="button" class="btn btn-small btn-link-danger" @click="revoking = inv; revokeAction.reset()">
                {{ $t('admin.invites.revoke') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ConfirmDialog
      :open="revoking !== null"
      :title="$t('admin.invites.revokeTitle')"
      :message="$t('admin.invites.revokeBody')"
      :confirm-label="$t('admin.invites.revoke')"
      danger
      :busy="revokeAction.busy.value"
      :error="revokeAction.error.value"
      @confirm="confirmRevoke"
      @cancel="revoking = null"
    />
  </AdminLayout>
</template>
