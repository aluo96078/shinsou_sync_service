<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { listDevices, renameDevice, signOutDevice } from '@/api/endpoints'
import { clearSession } from '@/api/session'
import type { Device } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { errorMessage, formatDateTime, hasMessage, t } from '@/i18n'
import { useAction } from '@/lib/useAction'

const router = useRouter()
const devices = ref<Device[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    devices.value = (await listDevices()).devices
  } catch (e) {
    loadError.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function platformLabel(p: string): string {
  const key = `platform.${p}`
  return hasMessage(key) ? t(key) : p
}

// --- rename ------------------------------------------------------------------------------------
const renaming = ref<Device | null>(null)
const newName = ref('')
const renameAction = useAction()

function startRename(d: Device): void {
  renaming.value = d
  newName.value = d.name
  renameAction.reset()
}
async function confirmRename(): Promise<void> {
  const d = renaming.value
  if (!d) return
  const updated = await renameAction.run(() => renameDevice(d.id, newName.value.trim()))
  if (updated) {
    devices.value = devices.value.map((x) => (x.id === updated.id ? updated : x))
    renaming.value = null
  }
}

// --- sign out ----------------------------------------------------------------------------------
const signingOut = ref<Device | null>(null)
const signOutAction = useAction()

function startSignOut(d: Device): void {
  signingOut.value = d
  signOutAction.reset()
}
async function confirmSignOut(): Promise<void> {
  const d = signingOut.value
  if (!d) return
  const ok = await signOutAction.run(async () => {
    await signOutDevice(d.id)
    return true
  })
  if (!ok) return
  signingOut.value = null
  if (d.current) {
    clearSession()
    await router.replace({ name: 'login' })
  } else {
    devices.value = devices.value.filter((x) => x.id !== d.id)
  }
}
</script>

<template>
  <div>
    <div class="page-head"><h1>{{ $t('devices.title') }}</h1></div>
    <p class="muted">{{ $t('devices.intro') }}</p>

    <ErrorNote :message="loadError" />
    <p v-if="loading" class="muted" role="status">{{ $t('app.loading') }}</p>
    <p v-else-if="devices.length === 0 && !loadError" class="muted">{{ $t('devices.empty') }}</p>

    <div v-else-if="devices.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">{{ $t('devices.col.name') }}</th>
            <th scope="col">{{ $t('devices.col.platform') }}</th>
            <th scope="col">{{ $t('devices.col.version') }}</th>
            <th scope="col">{{ $t('devices.col.created') }}</th>
            <th scope="col">{{ $t('devices.col.lastSeen') }}</th>
            <th scope="col"><span class="sr-only">{{ $t('common.actions') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in devices" :key="d.id">
            <td>
              {{ d.name }}
              <span v-if="d.current" class="badge badge-accent">{{ $t('devices.thisBrowser') }}</span>
            </td>
            <td>{{ platformLabel(d.platform) }}</td>
            <td>{{ d.appVersion || '—' }}</td>
            <td class="nowrap">{{ formatDateTime(d.createdAt) }}</td>
            <td class="nowrap">{{ formatDateTime(d.lastSeenAt) }}</td>
            <td>
              <div class="cell-actions">
                <button type="button" class="btn btn-small" @click="startRename(d)">{{ $t('devices.rename') }}</button>
                <button type="button" class="btn btn-small btn-link-danger" @click="startSignOut(d)">{{ $t('devices.signOut') }}</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ConfirmDialog
      :open="renaming !== null"
      :title="$t('devices.renameTitle')"
      :confirm-label="$t('common.save')"
      :busy="renameAction.busy.value"
      :error="renameAction.error.value"
      :can-confirm="newName.trim().length > 0"
      @confirm="confirmRename"
      @cancel="renaming = null"
    >
      <FormField :label="$t('devices.name')">
        <template #default="{ id }">
          <input :id="id" v-model="newName" type="text" maxlength="64" required />
        </template>
      </FormField>
    </ConfirmDialog>

    <ConfirmDialog
      :open="signingOut !== null"
      :title="$t('devices.signOutTitle')"
      :message="signingOut?.current ? $t('devices.signOutCurrentBody') : $t('devices.signOutBody', { name: signingOut?.name ?? '' })"
      :confirm-label="$t('devices.signOut')"
      danger
      :busy="signOutAction.busy.value"
      :error="signOutAction.error.value"
      @confirm="confirmSignOut"
      @cancel="signingOut = null"
    />
  </div>
</template>
