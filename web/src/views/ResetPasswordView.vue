<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ApiError } from '@/api/client'
import { resetPassword } from '@/api/endpoints'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { t } from '@/i18n'
import { consumeHashToken, parseHashToken } from '@/lib/hashToken'
import { useAction } from '@/lib/useAction'

const route = useRoute()
const token = ref<string | null>(null)
const password = ref('')
const confirm = ref('')
const localError = ref<string | null>(null)
const done = ref(false)
const invalid = ref(false)
const { busy, error, run } = useAction()

onMounted(() => {
  // The token is read once and then removed from the address bar; it is only kept in memory.
  token.value = consumeHashToken() ?? parseHashToken(route.hash)
  invalid.value = token.value === null
})

// Opening another link while this page is already showing only changes the fragment.
watch(
  () => route.hash,
  (hash) => {
    const next = parseHashToken(hash)
    if (!next) return
    consumeHashToken()
    token.value = next
    invalid.value = false
    done.value = false
  },
)

async function submit(): Promise<void> {
  localError.value = null
  if (!token.value) return
  if (password.value.length < 8) {
    localError.value = t('validation.passwordShort')
    return
  }
  if (password.value !== confirm.value) {
    localError.value = t('validation.passwordMismatch')
    return
  }
  const ok = await run(async () => {
    try {
      await resetPassword(token.value as string, password.value)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'invalid_token') invalid.value = true
      throw e
    }
    return true
  })
  if (ok) {
    done.value = true
    token.value = null
  }
}
</script>

<template>
  <section class="card card-narrow" aria-labelledby="reset-title">
    <h1 id="reset-title">{{ $t('reset.title') }}</h1>
    <template v-if="done">
      <ErrorNote :message="$t('reset.done')" kind="success" />
      <RouterLink class="btn btn-primary" :to="{ name: 'login' }">{{ $t('reset.goSignIn') }}</RouterLink>
    </template>
    <template v-else-if="invalid">
      <ErrorNote :message="$t('reset.invalid')" />
      <RouterLink class="btn" :to="{ name: 'forgot-password' }">{{ $t('reset.requestNew') }}</RouterLink>
    </template>
    <form v-else @submit.prevent="submit">
      <FormField :label="$t('field.newPassword')" :hint="$t('hint.password')">
        <template #default="{ id, describedby }">
          <input :id="id" v-model="password" type="password" autocomplete="new-password" minlength="8" maxlength="256" required :aria-describedby="describedby" />
        </template>
      </FormField>
      <FormField :label="$t('field.confirmPassword')">
        <template #default="{ id }">
          <input :id="id" v-model="confirm" type="password" autocomplete="new-password" required />
        </template>
      </FormField>
      <ErrorNote :message="localError ?? error" />
      <button type="submit" class="btn btn-primary" style="width: 100%" :disabled="busy">
        {{ $t('reset.submit') }}
      </button>
    </form>
  </section>
</template>
