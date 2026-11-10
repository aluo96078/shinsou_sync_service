<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { adminOverview } from '@/api/endpoints'
import type { AdminOverview } from '@/api/types'
import AdminLayout from '@/components/AdminLayout.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import { errorMessage, formatNumber } from '@/i18n'

const data = ref<AdminOverview | null>(null)
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    data.value = await adminOverview()
  } catch (e) {
    error.value = errorMessage(e)
  }
})
</script>

<template>
  <AdminLayout :title="$t('admin.title')">
    <ErrorNote :message="error" />
    <p v-if="!data && !error" class="muted" role="status">{{ $t('app.loading') }}</p>
    <template v-if="data">
      <ul class="stats">
        <li class="stat"><div class="stat-value">{{ formatNumber(data.users.total) }}</div><div class="stat-label">{{ $t('admin.overview.users') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.users.active) }}</div><div class="stat-label">{{ $t('admin.overview.active') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.users.suspended) }}</div><div class="stat-label">{{ $t('admin.overview.suspended') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.users.admins) }}</div><div class="stat-label">{{ $t('admin.overview.admins') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.devices) }}</div><div class="stat-label">{{ $t('admin.overview.devices') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.records) }}</div><div class="stat-label">{{ $t('admin.overview.records') }}</div></li>
        <li class="stat"><div class="stat-value">{{ formatNumber(data.works) }}</div><div class="stat-label">{{ $t('admin.overview.works') }}</div></li>
      </ul>
      <dl class="facts">
        <dt>{{ $t('admin.overview.version') }}</dt>
        <dd class="mono">{{ data.version }}</dd>
        <dt>{{ $t('admin.overview.email') }}</dt>
        <dd>
          <span class="badge" :class="data.emailAvailable ? 'badge-ok' : 'badge-warn'">
            {{ data.emailAvailable ? $t('admin.overview.emailOn') : $t('admin.overview.emailOff') }}
          </span>
        </dd>
      </dl>
    </template>
  </AdminLayout>
</template>
