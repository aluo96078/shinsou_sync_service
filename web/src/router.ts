import {
  createRouter,
  createWebHistory,
  type RouteLocationNormalized,
  type RouteLocationRaw,
  type RouteRecordRaw,
} from 'vue-router'
import { sessionReady } from '@/api/auth'
import { session } from '@/api/session'
import { redirectOrHome } from '@/lib/redirect'
import { loadInstance, visitorBrowsingFeature } from '@/stores/instance'

declare module 'vue-router' {
  interface RouteMeta {
    /** Needs a signed-in user. */
    auth?: boolean
    /** Only for signed-out visitors (login, register, ...). */
    guest?: boolean
    /** Needs role admin; everybody else gets the not-found page. */
    admin?: boolean
    /** Needs role moderator or admin; everybody else gets the not-found page. */
    moderator?: boolean
    /** Open to signed-out visitors while the server allows visitor browsing; otherwise it needs a sign-in. */
    visitor?: boolean
  }
}

const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { guest: true } },
  { path: '/register', name: 'register', component: () => import('@/views/RegisterView.vue'), meta: { guest: true } },
  { path: '/forgot-password', name: 'forgot-password', component: () => import('@/views/ForgotPasswordView.vue'), meta: { guest: true } },
  { path: '/reset-password', name: 'reset-password', component: () => import('@/views/ResetPasswordView.vue') },
  { path: '/verify-email', name: 'verify-email', component: () => import('@/views/VerifyEmailView.vue') },
  { path: '/', name: 'library', component: () => import('@/views/LibraryView.vue'), meta: { auth: true } },
  { path: '/library/:key(.*)', name: 'library-detail', component: () => import('@/views/LibraryDetailView.vue'), meta: { auth: true } },
  { path: '/account', name: 'account', component: () => import('@/views/AccountView.vue'), meta: { auth: true } },
  { path: '/devices', name: 'devices', component: () => import('@/views/DevicesView.vue'), meta: { auth: true } },
  { path: '/shares', name: 'shares', component: () => import('@/views/SharesView.vue'), props: { box: 'inbox' }, meta: { auth: true } },
  { path: '/shares/sent', name: 'shares-sent', component: () => import('@/views/SharesView.vue'), props: { box: 'sent' }, meta: { auth: true } },
  { path: '/people', name: 'people', component: () => import('@/views/PeopleView.vue'), meta: { auth: true } },
  { path: '/works/:hash([0-9a-f]{64})', name: 'work', component: () => import('@/views/WorkView.vue'), meta: { visitor: true } },
  { path: '/reviews', name: 'my-reviews', component: () => import('@/views/MyReviewsView.vue'), meta: { auth: true } },
  { path: '/mod', name: 'mod', component: () => import('@/views/ModView.vue'), meta: { auth: true, moderator: true } },
  { path: '/admin', name: 'admin', component: () => import('@/views/admin/AdminOverviewView.vue'), meta: { auth: true, admin: true } },
  { path: '/admin/users', name: 'admin-users', component: () => import('@/views/admin/AdminUsersView.vue'), meta: { auth: true, admin: true } },
  { path: '/admin/invites', name: 'admin-invites', component: () => import('@/views/admin/AdminInvitesView.vue'), meta: { auth: true, admin: true } },
  { path: '/admin/settings', name: 'admin-settings', component: () => import('@/views/admin/AdminSettingsView.vue'), meta: { auth: true, admin: true } },
  { path: '/admin/audit', name: 'admin-audit', component: () => import('@/views/admin/AdminAuditView.vue'), meta: { auth: true, admin: true } },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('@/views/NotFoundView.vue') },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior(to, from, saved) {
    if (saved) return saved
    if (to.path !== from.path) return { top: 0 }
    return undefined
  },
})

/** Where a not-signed-in visitor of `fullPath` should go. */
export function loginLocation(fullPath: string): RouteLocationRaw {
  const dest = redirectOrHome(fullPath.split('#')[0])
  return dest === '/' ? { name: 'login' } : { name: 'login', query: { redirect: dest } }
}

/** Shows the not-found page without changing the address. */
function notFoundLocation(to: RouteLocationNormalized): RouteLocationRaw {
  return {
    name: 'not-found',
    params: { pathMatch: to.path.substring(1).split('/') },
    query: to.query,
    hash: to.hash,
  }
}

router.beforeEach(async (to) => {
  await sessionReady()
  const user = session.user
  if (to.meta.auth && !user) return loginLocation(to.fullPath)
  if (to.meta.guest && user) return redirectOrHome(to.query.redirect)
  if (to.meta.visitor && !user) {
    // Signed-out visitors only get in while the server allows it.
    await loadInstance()
    if (!visitorBrowsingFeature.value) return loginLocation(to.fullPath)
  }
  if (to.meta.admin && user?.role !== 'admin') return notFoundLocation(to)
  if (to.meta.moderator && user?.role !== 'admin' && user?.role !== 'moderator') return notFoundLocation(to)
  return true
})
