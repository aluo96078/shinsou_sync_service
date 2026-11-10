<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { reloadMe } from '@/api/auth'
import { verifyEmail } from '@/api/endpoints'
import { session } from '@/api/session'
import ErrorNote from '@/components/ErrorNote.vue'
import { errorMessage } from '@/i18n'
import { consumeHashToken, parseHashToken } from '@/lib/hashToken'

const route = useRoute()
type State = 'working' | 'done' | 'invalid' | 'missing' | 'error'
const state = ref<State>('working')
const message = ref<string | null>(null)

async function verify(token: string | null): Promise<void> {
  message.value = null
  if (!token) {
    state.value = 'missing'
    return
  }
  state.value = 'working'
  try {
    await verifyEmail(token)
  } catch (e) {
    const code = (e as { code?: string }).code
    if (code === 'invalid_token') {
      state.value = 'invalid'
    } else {
      state.value = 'error'
      message.value = errorMessage(e)
    }
    return
  }
  if (session.user) await reloadMe().catch(() => undefined)
  state.value = 'done'
}

onMounted(() => verify(consumeHashToken() ?? parseHashToken(route.hash)))

// Opening another link while this page is already showing only changes the fragment.
watch(
  () => route.hash,
  (hash) => {
    const token = parseHashToken(hash)
    if (!token) return
    consumeHashToken()
    void verify(token)
  },
)
</script>

<template>
  <section class="card card-narrow" aria-labelledby="verify-title">
    <h1 id="verify-title">{{ $t('verify.title') }}</h1>
    <p v-if="state === 'working'" role="status" class="muted">{{ $t('verify.working') }}</p>
    <ErrorNote v-else-if="state === 'done'" :message="$t('verify.done')" kind="success" />
    <ErrorNote v-else-if="state === 'invalid'" :message="$t('verify.invalid')" />
    <ErrorNote v-else-if="state === 'missing'" :message="$t('verify.missing')" />
    <ErrorNote v-else :message="message" />

    <div v-if="state !== 'working'" class="form-actions">
      <RouterLink v-if="session.user" class="btn btn-primary" :to="{ name: state === 'done' ? 'library' : 'account' }">
        {{ state === 'done' ? $t('verify.continue') : $t('nav.account') }}
      </RouterLink>
      <RouterLink v-else class="btn btn-primary" :to="{ name: 'login' }">{{ $t('verify.goSignIn') }}</RouterLink>
    </div>
  </section>
</template>
