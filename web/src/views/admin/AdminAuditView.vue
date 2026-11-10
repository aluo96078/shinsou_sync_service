<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { adminListAudit } from '@/api/endpoints'
import type { AuditEntry } from '@/api/types'
import AdminLayout from '@/components/AdminLayout.vue'
import ErrorNote from '@/components/ErrorNote.vue'
import Pager from '@/components/Pager.vue'
import { errorMessage, formatDateTime } from '@/i18n'

const LIMIT = 50
const items = ref<AuditEntry[]>([])
const total = ref(0)
const offset = ref(0)
const loading = ref(true)
const error = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  error.value = null
  try {
    const page = await adminListAudit({ offset: offset.value, limit: LIMIT })
    items.value = page.items
    total.value = page.total
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function setOffset(n: number): void {
  offset.value = n
  void load()
}

function target(e: AuditEntry): string {
  if (!e.targetType && !e.targetId) return '—'
  return [e.targetType, e.targetId].filter(Boolean).join(' ')
}
function detail(e: AuditEntry): string {
  return e.detail ? JSON.stringify(e.detail, null, 2) : ''
}
</script>

<template>
  <AdminLayout :title="$t('admin.nav.audit')">
    <ErrorNote :message="error" />
    <p v-if="loading && items.length === 0" class="muted" role="status">{{ $t('app.loading') }}</p>
    <p v-else-if="!error && items.length === 0" class="muted">{{ $t('admin.audit.empty') }}</p>
    <div v-else class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th scope="col">{{ $t('admin.audit.col.time') }}</th>
            <th scope="col">{{ $t('admin.audit.col.actor') }}</th>
            <th scope="col">{{ $t('admin.audit.col.action') }}</th>
            <th scope="col">{{ $t('admin.audit.col.target') }}</th>
            <th scope="col">{{ $t('admin.audit.col.detail') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="e in items" :key="e.id">
            <td class="nowrap">{{ formatDateTime(e.createdAt) }}</td>
            <td>{{ e.actor?.username ?? $t('admin.audit.system') }}</td>
            <td class="mono">{{ e.action }}</td>
            <td class="mono" style="overflow-wrap: anywhere">{{ target(e) }}</td>
            <td><pre v-if="e.detail" class="json">{{ detail(e) }}</pre><span v-else class="muted">—</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pager v-if="total > 0" :total="total" :offset="offset" :limit="LIMIT" :busy="loading" @update:offset="setOffset" />
  </AdminLayout>
</template>
