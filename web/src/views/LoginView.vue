<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { login } from '@/api/auth'
import ErrorNote from '@/components/ErrorNote.vue'
import FormField from '@/components/FormField.vue'
import { useAction } from '@/lib/useAction'
import { redirectOrHome } from '@/lib/redirect'
import { canRegister, emailFeature, instance, instanceName } from '@/stores/instance'

const route = useRoute()
const router = useRouter()
const username = ref('')
const password = ref('')
const { busy, error, run } = useAction()

async function submit(): Promise<void> {
  const ok = await run(async () => {
    await login({ username: username.value.trim(), password: password.value })
    return true
  })
  if (ok) await router.replace(redirectOrHome(route.query.redirect))
}
</script>

<template>
  <section class="card card-narrow" aria-labelledby="login-title">
    <div class="auth-intro">
      <h1 id="login-title">{{ instanceName }}</h1>
      <p v-if="instance?.description" class="muted">{{ instance.description }}</p>
    </div>
    <h2 class="sr-only">{{ $t('login.title') }}</h2>
    <form @submit.prevent="submit">
      <FormField :label="$t('field.username')">
        <template #default="{ id }">
          <input :id="id" v-model="username" type="text" name="username" autocomplete="username" autocapitalize="off" spellcheck="false" required />
        </template>
      </FormField>
      <FormField :label="$t('field.password')">
        <template #default="{ id }">
          <input :id="id" v-model="password" type="password" name="password" autocomplete="current-password" required />
        </template>
      </FormField>
      <ErrorNote :message="error" />
      <button type="submit" class="btn btn-primary" style="width: 100%" :disabled="busy">
        {{ busy ? $t('login.submitting') : $t('login.submit') }}
      </button>
    </form>
    <div class="auth-links">
      <span v-if="canRegister">
        {{ $t('login.noAccount') }}
        <RouterLink :to="{ name: 'register', query: route.query.redirect ? { redirect: route.query.redirect } : {} }">
          {{ $t('login.register') }}
        </RouterLink>
      </span>
      <RouterLink v-if="emailFeature" :to="{ name: 'forgot-password' }">{{ $t('login.forgot') }}</RouterLink>
    </div>
  </section>
</template>
