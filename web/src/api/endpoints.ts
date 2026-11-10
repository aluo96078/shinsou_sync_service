import { json, noContent, request } from './client'
import { readCsrfCookie } from '@/lib/cookies'
import type {
  AdminOverview,
  AdminSettings,
  AdminUser,
  AdminUserPatch,
  AuditEntry,
  Category,
  Device,
  Instance,
  InstanceSettingsPatch,
  Invite,
  LibraryDetail,
  LibraryPage,
  Paged,
  ReceiveFrom,
  Share,
  SharePage,
  ShareRequest,
  ShareStatus,
  SharingPreferences,
  Usage,
  User,
  UserList,
  UserPage,
  WebLoginRequest,
  WebRegisterRequest,
  WebSession,
} from './types'

const V1 = '/api/v1'

// --- instance / web session ------------------------------------------------------------------

export const getInstance = () =>
  json<Instance>('GET', '/.well-known/shinsou', { auth: false })

export const webLogin = (body: WebLoginRequest) =>
  json<WebSession>('POST', `${V1}/web/login`, { body, auth: false })

export const webRegister = (body: WebRegisterRequest) =>
  json<WebSession>('POST', `${V1}/web/register`, { body, auth: false })

export const webLogout = () => {
  const csrf = readCsrfCookie()
  return noContent('POST', `${V1}/web/logout`, {
    auth: false,
    headers: csrf ? { 'X-CSRF-Token': csrf } : {},
  })
}

// --- account ---------------------------------------------------------------------------------

export const getMe = () => json<User>('GET', `${V1}/me`)

export const changePassword = (currentPassword: string, newPassword: string) =>
  noContent('POST', `${V1}/me/password`, { body: { currentPassword, newPassword } })

export const changeEmail = (email: string | null, currentPassword: string) =>
  json<User>('PUT', `${V1}/me/email`, { body: { email, currentPassword } })

export const resendVerification = () => noContent('POST', `${V1}/me/email/verification`)

export const getUsage = () => json<Usage>('GET', `${V1}/me/usage`)

export const exportAccount = () => request('GET', `${V1}/me/export`)

export const deleteAccount = (currentPassword: string) =>
  noContent('POST', `${V1}/me/delete`, { body: { currentPassword } })

export const verifyEmail = (token: string) =>
  noContent('POST', `${V1}/auth/email/verify`, { body: { token }, auth: false })

export const forgotPassword = (email: string) =>
  noContent('POST', `${V1}/auth/password/forgot`, { body: { email }, auth: false })

export const resetPassword = (token: string, newPassword: string) =>
  noContent('POST', `${V1}/auth/password/reset`, { body: { token, newPassword }, auth: false })

// --- devices ---------------------------------------------------------------------------------

export const listDevices = () => json<{ devices: Device[] }>('GET', `${V1}/devices`)

export const renameDevice = (id: string, name: string) =>
  json<Device>('PATCH', `${V1}/devices/${encodeURIComponent(id)}`, { body: { name } })

export const signOutDevice = (id: string) =>
  noContent('DELETE', `${V1}/devices/${encodeURIComponent(id)}`)

// --- library ---------------------------------------------------------------------------------

export interface LibraryQuery {
  q?: string
  category?: string
  archived?: 'exclude' | 'include' | 'only'
  sort?: 'recent' | 'title' | 'added'
  offset?: number
  limit?: number
}

export const listLibrary = (query: LibraryQuery, signal?: AbortSignal) =>
  json<LibraryPage>('GET', `${V1}/library`, signal ? { query: { ...query }, signal } : { query: { ...query } })

export const getLibraryEntry = (key: string) =>
  json<LibraryDetail>('GET', `${V1}/library/${encodeURIComponent(key)}`)

export const listCategories = () => json<{ categories: Category[] }>('GET', `${V1}/categories`)

// --- social: people ---------------------------------------------------------------------------

const userPath = (list: 'following' | 'blocks', id: string) =>
  `${V1}/me/${list}/${encodeURIComponent(id)}`

export const searchUsers = (q: string, limit?: number, signal?: AbortSignal) =>
  json<UserList>('GET', `${V1}/users`, signal ? { query: { q, limit }, signal } : { query: { q, limit } })

export type Relation = 'following' | 'followers' | 'blocks'

export const listRelation = (relation: Relation, query: { offset?: number; limit?: number }) =>
  json<UserPage>('GET', `${V1}/me/${relation}`, { query: { ...query } })

export const followUser = (id: string) => noContent('PUT', userPath('following', id))
export const unfollowUser = (id: string) => noContent('DELETE', userPath('following', id))
export const blockUser = (id: string) => noContent('PUT', userPath('blocks', id))
export const unblockUser = (id: string) => noContent('DELETE', userPath('blocks', id))

export const getSharingPreferences = () => json<SharingPreferences>('GET', `${V1}/me/sharing`)

export const setSharingPreferences = (receiveFrom: ReceiveFrom) =>
  json<SharingPreferences>('PUT', `${V1}/me/sharing`, { body: { receiveFrom } })

// --- social: shares ---------------------------------------------------------------------------

export interface ShareQuery {
  box?: 'inbox' | 'sent'
  status?: ShareStatus
  offset?: number
  limit?: number
}

export const listShares = (query: ShareQuery) =>
  json<SharePage>('GET', `${V1}/shares`, { query: { ...query } })

export const createShare = (body: ShareRequest) => json<Share>('POST', `${V1}/shares`, { body })

export const getShareSummary = () => json<{ pending: number }>('GET', `${V1}/shares/summary`)

const sharePath = (id: string) => `${V1}/shares/${encodeURIComponent(id)}`

export const acceptShare = (id: string) => json<Share>('POST', `${sharePath(id)}/accept`)
export const dismissShare = (id: string) => json<Share>('POST', `${sharePath(id)}/dismiss`)
export const deleteShare = (id: string) => noContent('DELETE', sharePath(id))

// --- admin -----------------------------------------------------------------------------------

export const adminOverview = () => json<AdminOverview>('GET', `${V1}/admin/overview`)

export const adminListUsers = (query: { q?: string; offset?: number; limit?: number }) =>
  json<Paged<AdminUser>>('GET', `${V1}/admin/users`, { query: { ...query } })

export const adminUpdateUser = (id: string, patch: AdminUserPatch) =>
  json<AdminUser>('PATCH', `${V1}/admin/users/${encodeURIComponent(id)}`, { body: patch })

export const adminDeleteUser = (id: string) =>
  noContent('DELETE', `${V1}/admin/users/${encodeURIComponent(id)}`)

export const adminSignOutUser = (id: string) =>
  noContent('POST', `${V1}/admin/users/${encodeURIComponent(id)}/sign-out`)

export const adminListInvites = () => json<{ invites: Invite[] }>('GET', `${V1}/admin/invites`)

export const adminCreateInvite = (body: {
  maxUses?: number
  expiresInHours?: number | null
  note?: string | null
}) => json<{ invite: Invite; code: string }>('POST', `${V1}/admin/invites`, { body })

export const adminDeleteInvite = (id: string) =>
  noContent('DELETE', `${V1}/admin/invites/${encodeURIComponent(id)}`)

export const adminGetSettings = () => json<AdminSettings>('GET', `${V1}/admin/settings`)

export const adminUpdateSettings = (patch: InstanceSettingsPatch) =>
  json<AdminSettings>('PATCH', `${V1}/admin/settings`, { body: patch })

export const adminListAudit = (query: { offset?: number; limit?: number }) =>
  json<Paged<AuditEntry>>('GET', `${V1}/admin/audit`, { query: { ...query } })
