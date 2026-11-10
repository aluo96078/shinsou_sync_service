<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { adminGetSettings, adminUpdateSettings } from '@/api/endpoints'
import type {
  AdminSettings,
  EmailVerification,
  InstanceSettings,
  InstanceSettingsPatch,
  Registration,
} from '@/api/types'
import AdminLayout from '@/components/AdminLayout.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import SettingRow from '@/components/SettingRow.vue'
import { errorMessage, t } from '@/i18n'
import { useAction } from '@/lib/useAction'
import { loadInstance } from '@/stores/instance'

const REGISTRATIONS: readonly Registration[] = ['open', 'invite', 'closed']
const VERIFICATIONS: readonly EmailVerification[] = ['disabled', 'optional', 'required']

const settings = ref<AdminSettings | null>(null)
const loadError = ref<string | null>(null)
const saved = ref<string | null>(null)
const saveAction = useAction()
const resetAction = useAction()

const form = reactive({
  name: '',
  description: '',
  registration: 'open' as Registration,
  emailVerification: 'disabled' as EmailVerification,
  termsUrl: '',
  privacyUrl: '',
  contact: '',
})

function fillForm(v: InstanceSettings): void {
  form.name = v.name
  form.description = v.description
  form.registration = v.registration
  form.emailVerification = v.emailVerification
  form.termsUrl = v.termsUrl ?? ''
  form.privacyUrl = v.privacyUrl ?? ''
  form.contact = v.contact ?? ''
}

function apply(s: AdminSettings): void {
  settings.value = s
  fillForm(s.values)
}

onMounted(async () => {
  try {
    apply(await adminGetSettings())
  } catch (e) {
    loadError.value = errorMessage(e)
  }
})

function isOverridden(key: keyof InstanceSettings): boolean {
  return settings.value?.overridden.includes(key) ?? false
}

function defaultText(key: keyof InstanceSettings): string {
  const d = settings.value?.defaults
  if (!d) return ''
  switch (key) {
    case 'registration':
      return t(`registration.${d.registration}`)
    case 'emailVerification':
      return t(`emailVerification.${d.emailVerification}`)
    default:
      return d[key] ? String(d[key]) : t('admin.settings.empty')
  }
}

async function afterChange(s: AdminSettings): Promise<void> {
  apply(s)
  saved.value = t('admin.settings.saved')
  await loadInstance(true)
}

async function save(): Promise<void> {
  saved.value = null
  const v = settings.value?.values
  if (!v) return
  const patch: InstanceSettingsPatch = {}
  if (form.name !== v.name) patch.name = form.name
  if (form.description !== v.description) patch.description = form.description
  if (form.registration !== v.registration) patch.registration = form.registration
  if (form.emailVerification !== v.emailVerification) patch.emailVerification = form.emailVerification
  if (form.termsUrl !== (v.termsUrl ?? '')) patch.termsUrl = form.termsUrl
  if (form.privacyUrl !== (v.privacyUrl ?? '')) patch.privacyUrl = form.privacyUrl
  if (form.contact !== (v.contact ?? '')) patch.contact = form.contact
  if (Object.keys(patch).length === 0) {
    saved.value = t('admin.settings.noChanges')
    return
  }
  const res = await saveAction.run(() => adminUpdateSettings(patch))
  if (res) await afterChange(res)
}

async function reset(key: keyof InstanceSettings): Promise<void> {
  saved.value = null
  saveAction.reset()
  const patch: InstanceSettingsPatch = { [key]: null }
  const res = await resetAction.run(() => adminUpdateSettings(patch))
  if (res) await afterChange(res)
}
</script>

<template>
  <AdminLayout :title="$t('admin.nav.settings')">
    <ErrorNote :message="loadError" />
    <p v-if="!settings && !loadError" class="muted" role="status">{{ $t('app.loading') }}</p>

    <form v-if="settings" class="card" @submit.prevent="save">
      <p class="muted">{{ $t('admin.settings.intro') }}</p>

      <SettingRow :label="$t('admin.settings.name')" :overridden="isOverridden('name')" :default-text="defaultText('name')" :busy="resetAction.busy.value" @reset="reset('name')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="form.name" type="text" minlength="1" maxlength="80" required :aria-describedby="describedby" />
        </template>
      </SettingRow>

      <SettingRow :label="$t('admin.settings.description')" :overridden="isOverridden('description')" :default-text="defaultText('description')" :busy="resetAction.busy.value" @reset="reset('description')">
        <template #default="{ id, describedby }">
          <textarea :id="id" v-model="form.description" maxlength="500" :aria-describedby="describedby"></textarea>
        </template>
      </SettingRow>

      <SettingRow :label="$t('admin.settings.registration')" :overridden="isOverridden('registration')" :default-text="defaultText('registration')" :busy="resetAction.busy.value" @reset="reset('registration')">
        <template #default="{ id, describedby }">
          <select :id="id" v-model="form.registration" :aria-describedby="describedby">
            <option v-for="r in REGISTRATIONS" :key="r" :value="r">{{ $t(`registration.${r}`) }}</option>
          </select>
        </template>
      </SettingRow>

      <SettingRow
        :label="$t('admin.settings.emailVerification')"
        :overridden="isOverridden('emailVerification')"
        :default-text="defaultText('emailVerification')"
        :hint="settings.emailAvailable ? undefined : $t('admin.settings.emailUnavailable')"
        :busy="resetAction.busy.value"
        @reset="reset('emailVerification')"
      >
        <template #default="{ id, describedby }">
          <select :id="id" v-model="form.emailVerification" :aria-describedby="describedby">
            <option
              v-for="p in VERIFICATIONS"
              :key="p"
              :value="p"
              :disabled="p === 'required' && !settings.emailAvailable && settings.values.emailVerification !== 'required'"
            >
              {{ $t(`emailVerification.${p}`) }}
            </option>
          </select>
        </template>
      </SettingRow>

      <SettingRow :label="$t('admin.settings.termsUrl')" :overridden="isOverridden('termsUrl')" :default-text="defaultText('termsUrl')" :hint="$t('admin.settings.urlHint')" :busy="resetAction.busy.value" @reset="reset('termsUrl')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="form.termsUrl" type="url" maxlength="2048" :aria-describedby="describedby" />
        </template>
      </SettingRow>

      <SettingRow :label="$t('admin.settings.privacyUrl')" :overridden="isOverridden('privacyUrl')" :default-text="defaultText('privacyUrl')" :hint="$t('admin.settings.urlHint')" :busy="resetAction.busy.value" @reset="reset('privacyUrl')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="form.privacyUrl" type="url" maxlength="2048" :aria-describedby="describedby" />
        </template>
      </SettingRow>

      <SettingRow :label="$t('admin.settings.contact')" :overridden="isOverridden('contact')" :default-text="defaultText('contact')" :busy="resetAction.busy.value" @reset="reset('contact')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="form.contact" type="text" maxlength="200" :aria-describedby="describedby" />
        </template>
      </SettingRow>

      <ErrorNote :message="saveAction.error.value ?? resetAction.error.value" />
      <ErrorNote :message="saved" kind="success" />
      <div class="form-actions">
        <button type="submit" class="btn btn-primary" :disabled="saveAction.busy.value">
          {{ saveAction.busy.value ? $t('common.saving') : $t('common.save') }}
        </button>
      </div>
    </form>
  </AdminLayout>
</template>
