<script setup lang="ts">
import { formatNumber } from '@/i18n'
import { pendingShares } from '@/stores/shares'

defineProps<{ title: string }>()
</script>

<template>
  <div>
    <div class="page-head"><h1>{{ title }}</h1></div>
    <nav class="subnav" :aria-label="$t('social.title')">
      <RouterLink :to="{ name: 'shares' }">
        {{ $t('social.nav.inbox') }}
        <span v-if="pendingShares > 0" class="badge badge-accent">{{ formatNumber(pendingShares) }}</span>
      </RouterLink>
      <RouterLink :to="{ name: 'shares-sent' }">{{ $t('social.nav.sent') }}</RouterLink>
      <RouterLink :to="{ name: 'people' }">{{ $t('social.nav.people') }}</RouterLink>
    </nav>
    <slot />
  </div>
</template>
