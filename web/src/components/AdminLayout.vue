<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<template>
  <div>
    <div class="page-head"><h1>{{ title }}</h1></div>
    <nav class="subnav" :aria-label="$t('admin.title')">
      <RouterLink :to="{ name: 'admin' }">{{ $t('admin.nav.overview') }}</RouterLink>
      <RouterLink :to="{ name: 'admin-users' }">{{ $t('admin.nav.users') }}</RouterLink>
      <RouterLink :to="{ name: 'admin-invites' }">{{ $t('admin.nav.invites') }}</RouterLink>
      <RouterLink :to="{ name: 'admin-settings' }">{{ $t('admin.nav.settings') }}</RouterLink>
      <RouterLink :to="{ name: 'admin-audit' }">{{ $t('admin.nav.audit') }}</RouterLink>
    </nav>
    <slot />
  </div>
</template>
