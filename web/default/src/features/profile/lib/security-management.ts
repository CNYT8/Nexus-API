import { isAxiosError } from 'axios'

export function securityErrorKey(error: unknown, fallback: string): string {
  if (!isAxiosError(error)) return fallback
  if (error.response?.status === 401 || error.response?.data?.code === 'AUTH_SESSION_INVALID') return '当前登录会话已失效，请重新登录'
  if (error.response?.data?.code === 'AUTH_SESSION_REQUIRED') return '请使用当前浏览器登录会话管理设备'
  if (error.response?.data?.code === 'AUTH_SESSION_NOT_ACTIVE') return '会话不存在或已失效，请刷新列表'
  return fallback
}

export const SECURITY_STATUS_LABELS = {
  active: '有效',
  revoked: '已撤销',
  expired: '已过期',
  invalidated: '安全设置变更后已失效',
} as const

export function personalTokenScopes(role: number) {
  const modules = ['self', 'token', 'task']
  if (role >= 10) modules.push('channel', 'models', 'user', 'redemption', 'subscription', 'ticket')
  if (role >= 100) modules.push('system')
  return modules.flatMap((module) => [`${module}:read`, `${module}:write`])
}

export function parsePersonalTokenScopes(raw: string): string[] {
  try {
    const scopes: unknown = JSON.parse(raw)
    return Array.isArray(scopes) ? scopes.filter((scope): scope is string => typeof scope === 'string') : []
  } catch {
    return []
  }
}
