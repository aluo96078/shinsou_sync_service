<script setup lang="ts">
import { ref } from 'vue'
import { ApiError } from '@/api/client'
import { forgotPassword } from '@/api/endpoints'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { useAction } from '@/lib/useAction'

const email = ref('')
const sent = ref(false)
const { busy, error, lastError, run } = useAction()

async function submit(): Promise<void> {
  const ok = await run(async () => {
    await forgotPassword(email.value.trim())
    return true
  })
  if (ok) sent.value = true
}
</script>

<template>
  <section class="card card-narrow" aria-labelledby="forgot-title">
    <h1 id="forgot-title">{{ $t('forgot.title') }}</h1>
    <template v-if="sent">
      <ErrorNote :message="$t('forgot.sent')" kind="success" />
    </template>
    <form v-else @submit.prevent="submit">
      <p class="muted">{{ $t('forgot.intro') }}</p>
      <FormField :label="$t('field.email')">
        <template #default="{ id }">
          <input :id="id" v-model="email" type="email" name="email" autocomplete="email" required />
        </template>
      </FormField>
      <ErrorNote :message="lastError instanceof ApiError && lastError.code === 'email_unavailable' ? $t('forgot.unavailable') : error" />
      <button type="submit" class="btn btn-primary" style="width: 100%" :disabled="busy">
        {{ $t('forgot.submit') }}
      </button>
    </form>
    <div class="auth-links">
      <RouterLink :to="{ name: 'login' }">{{ $t('forgot.back') }}</RouterLink>
    </div>
  </section>
</template>
