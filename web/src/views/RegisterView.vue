<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { register } from '@/api/auth'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { t } from '@/i18n'
import { redirectOrHome } from '@/lib/redirect'
import { useAction } from '@/lib/useAction'
import {
  canRegister,
  instance,
  instanceName,
  privacyUrl,
  registrationMode,
  setupPending,
  termsUrl,
  verificationPolicy,
} from '@/stores/instance'

const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const confirm = ref('')
const email = ref('')
const inviteCode = ref('')
const setupCode = ref('')
const setupToggle = ref(false)
const localError = ref<string | null>(null)
const { busy, error, run } = useAction()

const emailRequired = computed(() => verificationPolicy.value === 'required')
const showInvite = computed(() => registrationMode.value === 'invite')
const showSetup = computed(() => setupPending.value || setupToggle.value)
const contact = computed(() => instance.value?.contact ?? null)

async function submit(): Promise<void> {
  localError.value = null
  if (password.value.length < 8) {
    localError.value = t('validation.passwordShort')
    return
  }
  if (password.value !== confirm.value) {
    localError.value = t('validation.passwordMismatch')
    return
  }
  const ok = await run(async () => {
    await register({
      username: username.value.trim().toLowerCase(),
      password: password.value,
      email: email.value.trim() || null,
      inviteCode: showInvite.value ? inviteCode.value.trim() || null : null,
      setupCode: showSetup.value ? setupCode.value.trim() || null : null,
    })
    return true
  })
  if (ok) await router.replace(redirectOrHome(route.query.redirect))
}
</script>

<template>
  <section class="card card-narrow" aria-labelledby="register-title">
    <div class="auth-intro">
      <h1 id="register-title">{{ $t('register.title') }}</h1>
      <p class="muted">{{ instanceName }}</p>
    </div>

    <p v-if="!canRegister && !setupToggle" class="note note-info">{{ $t('register.closed') }}</p>
    <p v-if="setupPending" class="note note-info">{{ $t('register.setupPending') }}</p>
    <p v-else-if="showInvite" class="hint" style="margin-bottom: 1rem">{{ $t('register.inviteOnly') }}</p>

    <form @submit.prevent="submit">
      <FormField :label="$t('field.username')" :hint="$t('hint.username')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="username" type="text" name="username" autocomplete="username" autocapitalize="off" spellcheck="false" pattern="[A-Za-z0-9_]{3,32}" minlength="3" maxlength="32" required :aria-describedby="describedby" />
        </template>
      </FormField>
      <FormField :label="$t('field.password')" :hint="$t('hint.password')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="password" type="password" name="new-password" autocomplete="new-password" minlength="8" maxlength="256" required :aria-describedby="describedby" />
        </template>
      </FormField>
      <FormField :label="$t('field.confirmPassword')">
        <template #default="{ id }">
          <input :id="id" v-model="confirm" type="password" name="confirm-password" autocomplete="new-password" required />
        </template>
      </FormField>
      <FormField :label="$t('field.email')" :optional="!emailRequired">
        <template #default="{ id }">
          <input :id="id" v-model="email" type="email" name="email" autocomplete="email" :required="emailRequired" />
        </template>
      </FormField>
      <FormField v-if="showInvite" :label="$t('field.inviteCode')">
        <template #default="{ id }">
          <input :id="id" v-model="inviteCode" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" />
        </template>
      </FormField>

      <template v-if="showSetup">
        <FormField :label="$t('field.setupCode')" :hint="$t('hint.setupCode')">
          <template #default="{ id, describedby }">
            <input :id="id" v-model="setupCode" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" :required="setupPending" :aria-describedby="describedby" />
          </template>
        </FormField>
      </template>
      <label v-if="!setupPending" class="check">
        <input v-model="setupToggle" type="checkbox" />
        <span>{{ $t('register.setupToggle') }}</span>
      </label>

      <ErrorNote :message="localError ?? error" />
      <button type="submit" class="btn btn-primary" style="width: 100%" :disabled="busy || (!canRegister && !setupToggle)">
        {{ busy ? $t('register.submitting') : $t('register.submit') }}
      </button>
    </form>

    <p v-if="termsUrl || privacyUrl" class="hint" style="margin-top: 1rem">
      {{ $t('register.legal') }}
      <a v-if="termsUrl" :href="termsUrl" target="_blank" rel="noopener noreferrer">{{ $t('register.terms') }}</a>
      <template v-if="termsUrl && privacyUrl"> · </template>
      <a v-if="privacyUrl" :href="privacyUrl" target="_blank" rel="noopener noreferrer">{{ $t('register.privacy') }}</a>
    </p>
    <p v-if="contact" class="hint">{{ $t('register.contact', { contact }) }}</p>

    <div class="auth-links">
      <span>
        {{ $t('register.haveAccount') }}
        <RouterLink :to="{ name: 'login', query: route.query.redirect ? { redirect: route.query.redirect } : {} }">
          {{ $t('register.signIn') }}
        </RouterLink>
      </span>
    </div>
  </section>
</template>
