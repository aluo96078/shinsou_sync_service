// Aliases over the generated OpenAPI types. Nothing here is hand-written API shape.
import type { components } from './schema'

type S = components['schemas']

export type Instance = S['Instance']
export type User = S['User']
export type Device = S['Device']
export type WebSession = S['WebSession']
export type WebTokens = S['WebTokens']
export type WebRegisterRequest = S['WebRegisterRequest']
export type WebLoginRequest = S['WebLoginRequest']
export type Usage = S['Usage']
export type LibraryItem = S['LibraryItem']
export type LibraryPage = S['LibraryPage']
export type LibraryDetail = S['LibraryDetail']
export type LibraryUnit = S['LibraryUnit']
export type Category = S['Category']
export type AdminOverview = S['AdminOverview']
export type AdminUser = S['AdminUser']
export type AdminUserPatch = S['AdminUserPatch']
export type Invite = S['Invite']
export type InstanceSettings = S['InstanceSettings']
export type InstanceSettingsPatch = S['InstanceSettingsPatch']
export type AdminSettings = S['AdminSettings']
export type AuditEntry = S['AuditEntry']
export type ErrorBody = S['Error']
export type ErrorCode = ErrorBody['error']['code']

export type Role = User['role']
export type Registration = Instance['registration']
export type EmailVerification = Instance['emailVerification']

export type Paged<T> = { items: T[]; total: number; offset: number; limit: number }
