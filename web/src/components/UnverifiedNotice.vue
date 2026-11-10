<script setup lang="ts">
import { useResendVerification } from '@/lib/useResend'
import ErrorNote from './ErrorNote.vue'

const { busy, message, failed, resend } = useResendVerification()
</script>

<template>
  <section class="empty-state" aria-labelledby="unverified-title">
    <h2 id="unverified-title">{{ $t('unverified.title') }}</h2>
    <p>{{ $t('unverified.body') }}</p>
    <button type="button" class="btn btn-primary" :disabled="busy" @click="resend">
      {{ $t('banner.resend') }}
    </button>
    <ErrorNote :message="message" :kind="failed ? 'error' : 'success'" />
    <p>
      <RouterLink :to="{ name: 'account' }">{{ $t('nav.account') }}</RouterLink>
    </p>
  </section>
</template>
