<script setup lang="ts">
import { computed, ref, watch, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { logout } from '@/api/auth'
import { session } from '@/api/session'
import { LOCALES, LOCALE_NAMES, errorMessage, formatNumber, locale, setLocale, t, type Locale, type MessageKey } from '@/i18n'
import { THEMES, setTheme, theme, type ThemeChoice } from '@/stores/theme'
import { instanceName, sharingFeature, verificationPolicy } from '@/stores/instance'
import { clearPendingShares, pendingShares, refreshPendingShares } from '@/stores/shares'
import { useResendVerification } from '@/lib/useResend'

const route = useRoute()
const router = useRouter()

const user = computed(() => session.user)
const isAdmin = computed(() => user.value?.role === 'admin')

const titleKeys: Record<string, MessageKey> = {
  login: 'login.title',
  register: 'register.title',
  'forgot-password': 'forgot.title',
  'reset-password': 'reset.title',
  'verify-email': 'verify.title',
  library: 'library.title',
  account: 'account.title',
  devices: 'devices.title',
  shares: 'social.nav.inbox',
  'shares-sent': 'social.nav.sent',
  people: 'social.nav.people',
  admin: 'admin.title',
  'admin-users': 'admin.nav.users',
  'admin-invites': 'admin.nav.invites',
  'admin-settings': 'admin.nav.settings',
  'admin-audit': 'admin.nav.audit',
  'not-found': 'notFound.title',
}

watchEffect(() => {
  const key = typeof route.name === 'string' ? titleKeys[route.name] : undefined
  document.title = key ? `${t(key)} · ${instanceName.value}` : instanceName.value
})

// Badge for unanswered shares: reloaded on sign-in, when the feature is switched, and on every
// navigation (cheap, and the web has no push channel).
watch(
  [() => user.value?.id, sharingFeature, () => route.fullPath],
  ([id, on]) => {
    if (id && on) void refreshPendingShares()
    else clearPendingShares()
  },
  { immediate: true },
)

// Unverified-email banner: shown while the policy asks for verification and the address is open.
const showBanner = computed(() => {
  const u = user.value
  if (!u || u.emailVerified === true) return false
  if (verificationPolicy.value === 'disabled') return false
  return verificationPolicy.value === 'required' || !!u.email
})
const blocked = computed(() => verificationPolicy.value === 'required' && !isAdmin.value)
const noEmail = computed(() => !user.value?.email)

const { busy: resendBusy, message: resendMessage, failed: resendFailed, resend } =
  useResendVerification()

const signOutError = ref<string | null>(null)

async function signOut(): Promise<void> {
  signOutError.value = null
  try {
    await logout()
  } catch (e) {
    signOutError.value = errorMessage(e)
    return
  }
  await router.push({ name: 'login' })
}

function onLocale(e: Event): void {
  setLocale((e.target as HTMLSelectElement).value as Locale)
}
function onTheme(e: Event): void {
  setTheme((e.target as HTMLSelectElement).value as ThemeChoice)
}
</script>

<template>
  <a class="skip-link" href="#main">{{ t('app.skip') }}</a>
  <header class="topbar">
    <div class="topbar-inner">
      <RouterLink class="brand" to="/">{{ instanceName }}</RouterLink>
      <nav v-if="user" class="mainnav" :aria-label="t('nav.main')">
        <RouterLink :to="{ name: 'library' }">{{ t('nav.library') }}</RouterLink>
        <RouterLink v-if="sharingFeature" :to="{ name: 'shares' }" :class="{ 'router-link-active': route.name === 'people' }">
          {{ t('nav.sharing') }}
          <span v-if="pendingShares > 0" class="badge badge-accent" :aria-label="t('nav.pendingShares', { n: pendingShares })">{{ formatNumber(pendingShares) }}</span>
        </RouterLink>
        <RouterLink :to="{ name: 'devices' }">{{ t('nav.devices') }}</RouterLink>
        <RouterLink :to="{ name: 'account' }">{{ t('nav.account') }}</RouterLink>
        <RouterLink v-if="isAdmin" :to="{ name: 'admin' }">{{ t('nav.admin') }}</RouterLink>
      </nav>
      <div class="toolbar">
        <label class="sr-only" for="lang-select">{{ t('lang.label') }}</label>
        <select id="lang-select" class="select-compact" :value="locale" @change="onLocale">
          <option v-for="l in LOCALES" :key="l" :value="l">{{ LOCALE_NAMES[l] }}</option>
        </select>
        <label class="sr-only" for="theme-select">{{ t('theme.label') }}</label>
        <select id="theme-select" class="select-compact" :value="theme" @change="onTheme">
          <option v-for="th in THEMES" :key="th" :value="th">{{ t(`theme.${th}`) }}</option>
        </select>
        <button v-if="user" type="button" class="btn btn-small" @click="signOut">
          {{ t('nav.signOut') }}
        </button>
      </div>
    </div>
  </header>

  <div v-if="showBanner" class="banner" role="status">
    <div class="banner-inner">
      <p>
        <strong>{{ t('banner.unverified') }}</strong>
        {{ blocked ? (noEmail ? t('banner.noEmailBlocked') : t('banner.unverifiedBlocked')) : '' }}
        <span v-if="resendMessage" :class="resendFailed ? 'text-error' : ''">{{ resendMessage }}</span>
      </p>
      <RouterLink v-if="noEmail" class="btn btn-small" :to="{ name: 'account' }">
        {{ t('banner.addEmail') }}
      </RouterLink>
      <button v-else type="button" class="btn btn-small" :disabled="resendBusy" @click="resend">
        {{ t('banner.resend') }}
      </button>
    </div>
  </div>

  <div v-if="signOutError" class="banner" role="alert">
    <div class="banner-inner">
      <p class="text-error">{{ t('nav.signOutFailed') }} {{ signOutError }}</p>
    </div>
  </div>

  <main id="main" class="main" tabindex="-1">
    <RouterView />
  </main>
</template>
