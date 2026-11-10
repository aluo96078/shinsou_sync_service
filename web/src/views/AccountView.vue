<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { clearSession, session } from '@/api/session'
import {
  changeEmail,
  changePassword,
  deleteAccount,
  exportAccount,
  getUsage,
} from '@/api/endpoints'
import type { Usage } from '@/api/types'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { errorMessage, formatDate, formatNumber, formatPercent, t } from '@/i18n'
import { filenameFromDisposition } from '@/lib/contentDisposition'
import { useAction } from '@/lib/useAction'
import { useResendVerification } from '@/lib/useResend'
import { verificationPolicy } from '@/stores/instance'

const router = useRouter()
const user = computed(() => session.user)

// --- email -------------------------------------------------------------------------------------
const newEmail = ref('')
const emailPassword = ref('')
const emailAction = useAction()
const emailDone = ref<string | null>(null)
const resend = useResendVerification()
const canRemoveEmail = computed(() => !!user.value?.email && verificationPolicy.value !== 'required')

async function saveEmail(email: string | null): Promise<void> {
  emailDone.value = null
  const updated = await emailAction.run(() => changeEmail(email, emailPassword.value))
  if (updated) {
    session.user = updated
    emailPassword.value = ''
    newEmail.value = ''
    emailDone.value = email ? t('account.emailSaved') : t('account.emailRemoved')
  }
}

// --- password ----------------------------------------------------------------------------------
const curPassword = ref('')
const nextPassword = ref('')
const nextConfirm = ref('')
const pwLocalError = ref<string | null>(null)
const pwDone = ref<string | null>(null)
const pwAction = useAction()

async function savePassword(): Promise<void> {
  pwLocalError.value = null
  pwDone.value = null
  if (nextPassword.value.length < 8) {
    pwLocalError.value = t('validation.passwordShort')
    return
  }
  if (nextPassword.value !== nextConfirm.value) {
    pwLocalError.value = t('validation.passwordMismatch')
    return
  }
  const ok = await pwAction.run(async () => {
    await changePassword(curPassword.value, nextPassword.value)
    return true
  })
  if (ok) {
    curPassword.value = nextPassword.value = nextConfirm.value = ''
    pwDone.value = t('account.passwordSaved')
  }
}

// --- usage -------------------------------------------------------------------------------------
const usage = ref<Usage | null>(null)
const usageError = ref<string | null>(null)
const KINDS = ['library', 'unit', 'progress', 'history', 'category', 'repository', 'setting'] as const

onMounted(async () => {
  try {
    usage.value = await getUsage()
  } catch (e) {
    usageError.value = errorMessage(e)
  }
})

const quota = computed(() => (usage.value?.quota && usage.value.quota > 0 ? usage.value.quota : null))
const usageRatio = computed(() =>
  usage.value && quota.value ? Math.min(1, usage.value.total / quota.value) : 0,
)

// --- export ------------------------------------------------------------------------------------
const exportAction = useAction()

async function download(): Promise<void> {
  await exportAction.run(async () => {
    const res = await exportAccount()
    const blob = await res.blob()
    const name = filenameFromDisposition(res.headers.get('Content-Disposition'), 'shinsou-export.json')
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.append(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 2000)
  })
}

// --- delete ------------------------------------------------------------------------------------
const deleteOpen = ref(false)
const deletePassword = ref('')
const deleteAction = useAction()

function openDelete(): void {
  deletePassword.value = ''
  deleteAction.reset()
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const ok = await deleteAction.run(async () => {
    await deleteAccount(deletePassword.value)
    return true
  })
  if (ok) {
    deleteOpen.value = false
    clearSession()
    await router.replace({ name: 'login' })
  }
}
</script>

<template>
  <div v-if="user">
    <div class="page-head"><h1>{{ $t('account.title') }}</h1></div>

    <section class="card" aria-labelledby="profile-title">
      <h2 id="profile-title">{{ $t('account.profile') }}</h2>
      <dl class="facts">
        <dt>{{ $t('field.username') }}</dt>
        <dd>{{ user.username }}</dd>
        <template v-if="user.handle">
          <dt>{{ $t('account.handle') }}</dt>
          <dd class="mono">{{ user.handle }}</dd>
        </template>
        <dt>{{ $t('account.role') }}</dt>
        <dd>{{ $t(`role.${user.role}`) }}</dd>
        <dt>{{ $t('account.createdAt') }}</dt>
        <dd>{{ formatDate(user.createdAt) }}</dd>
      </dl>
    </section>

    <section class="card" aria-labelledby="email-title">
      <h2 id="email-title">{{ $t('account.email') }}</h2>
      <p v-if="user.email" class="row">
        <span>{{ user.email }}</span>
        <span class="badge" :class="user.emailVerified ? 'badge-ok' : 'badge-warn'">
          {{ user.emailVerified ? $t('account.verified') : $t('account.unverified') }}
        </span>
      </p>
      <p v-else class="muted">{{ $t('account.emailNone') }}</p>

      <div v-if="user.email && !user.emailVerified" class="stack" style="margin-bottom: 1rem">
        <button type="button" class="btn btn-small" :disabled="resend.busy.value" @click="resend.resend">
          {{ $t('account.resend') }}
        </button>
        <ErrorNote :message="resend.message.value" :kind="resend.failed.value ? 'error' : 'success'" />
      </div>

      <form @submit.prevent="saveEmail(newEmail.trim())">
        <h3>{{ $t('account.changeEmail') }}</h3>
        <FormField :label="$t('account.newEmail')" :hint="$t('account.emailHint')">
          <template #default="{ id, describedby }">
            <input :id="id" v-model="newEmail" type="email" autocomplete="email" required :aria-describedby="describedby" />
          </template>
        </FormField>
        <FormField :label="$t('field.currentPassword')">
          <template #default="{ id }">
            <input :id="id" v-model="emailPassword" type="password" autocomplete="current-password" required />
          </template>
        </FormField>
        <ErrorNote :message="emailAction.error.value" />
        <ErrorNote :message="emailDone" kind="success" />
        <div class="form-actions">
          <button type="submit" class="btn btn-primary" :disabled="emailAction.busy.value">{{ $t('common.save') }}</button>
          <button
            v-if="canRemoveEmail"
            type="button"
            class="btn"
            :disabled="emailAction.busy.value || !emailPassword"
            @click="saveEmail(null)"
          >
            {{ $t('account.removeEmail') }}
          </button>
        </div>
      </form>
    </section>

    <section class="card" aria-labelledby="password-title">
      <h2 id="password-title">{{ $t('account.password') }}</h2>
      <form @submit.prevent="savePassword">
        <FormField :label="$t('field.currentPassword')">
          <template #default="{ id }">
            <input :id="id" v-model="curPassword" type="password" autocomplete="current-password" required />
          </template>
        </FormField>
        <FormField :label="$t('field.newPassword')" :hint="$t('account.passwordHint')">
          <template #default="{ id, describedby }">
            <input :id="id" v-model="nextPassword" type="password" autocomplete="new-password" minlength="8" maxlength="256" required :aria-describedby="describedby" />
          </template>
        </FormField>
        <FormField :label="$t('field.confirmPassword')">
          <template #default="{ id }">
            <input :id="id" v-model="nextConfirm" type="password" autocomplete="new-password" required />
          </template>
        </FormField>
        <ErrorNote :message="pwLocalError ?? pwAction.error.value" />
        <ErrorNote :message="pwDone" kind="success" />
        <div class="form-actions">
          <button type="submit" class="btn btn-primary" :disabled="pwAction.busy.value">{{ $t('common.save') }}</button>
        </div>
      </form>
    </section>

    <section class="card" aria-labelledby="usage-title">
      <h2 id="usage-title">{{ $t('account.usage') }}</h2>
      <ErrorNote :message="usageError" />
      <template v-if="usage">
        <p>
          <strong v-if="quota">{{ $t('account.usageQuota', { total: formatNumber(usage.total), quota: formatNumber(quota) }) }}</strong>
          <strong v-else>{{ $t('account.usageRecords', { total: formatNumber(usage.total) }) }}</strong>
          <span v-if="quota" class="muted"> ({{ formatPercent(usageRatio) }})</span>
        </p>
        <div
          v-if="quota"
          class="progress progress-lg"
          role="progressbar"
          :aria-label="$t('account.usage')"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-valuenow="Math.round(usageRatio * 100)"
        >
          <span :style="{ width: `${usageRatio * 100}%` }"></span>
        </div>
        <p v-else class="muted">{{ $t('account.usageUnlimited') }}</p>
        <h3 style="margin-top: 1.25rem">{{ $t('account.byKind') }}</h3>
        <ul class="kind-list">
          <li v-for="k in KINDS" :key="k">
            {{ $t(`recordKind.${k}`) }}: <strong>{{ formatNumber(usage.byKind[k] ?? 0) }}</strong>
          </li>
        </ul>
        <p class="hint" style="margin-top: 0.75rem">{{ $t('account.usageNote') }}</p>
      </template>
    </section>

    <section class="card" aria-labelledby="export-title">
      <h2 id="export-title">{{ $t('account.export') }}</h2>
      <p class="muted">{{ $t('account.exportBody') }}</p>
      <ErrorNote :message="exportAction.error.value" />
      <button type="button" class="btn" :disabled="exportAction.busy.value" @click="download">
        {{ exportAction.busy.value ? $t('account.exporting') : $t('account.exportButton') }}
      </button>
    </section>

    <section class="card card-danger" aria-labelledby="danger-title">
      <h2 id="danger-title">{{ $t('account.danger') }}</h2>
      <h3>{{ $t('account.delete') }}</h3>
      <p class="muted">{{ $t('account.deleteBody') }}</p>
      <button type="button" class="btn btn-danger" @click="openDelete">{{ $t('account.deleteButton') }}</button>
    </section>

    <ConfirmDialog
      :open="deleteOpen"
      :title="$t('account.deleteTitle')"
      :message="$t('account.deleteBody')"
      :confirm-label="$t('account.deleteButton')"
      danger
      :busy="deleteAction.busy.value"
      :error="deleteAction.error.value"
      :expect-text="user.username"
      :expect-label="$t('account.deleteType', { username: user.username })"
      :can-confirm="deletePassword.length > 0"
      @confirm="confirmDelete"
      @cancel="deleteOpen = false"
    >
      <FormField :label="$t('field.password')">
        <template #default="{ id }">
          <input :id="id" v-model="deletePassword" type="password" autocomplete="current-password" />
        </template>
      </FormField>
    </ConfirmDialog>
  </div>
</template>
