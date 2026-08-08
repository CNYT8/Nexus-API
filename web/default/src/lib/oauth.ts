/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from './api'
import {
  getOAuthSessionStorage,
  markOAuthBindPopup,
} from '@/features/auth/lib/oauth-callback-mode'

// ============================================================================
// OAuth URL Builders
// ============================================================================

/**
 * Build GitHub OAuth URL
 */
export function buildGitHubOAuthUrl(clientId: string, state: string): string {
  return `https://github.com/login/oauth/authorize?client_id=${clientId}&state=${state}&scope=user:email`
}

/**
 * Build Discord OAuth URL
 */
export function buildDiscordOAuthUrl(clientId: string, state: string): string {
  const url = new URL('https://discord.com/oauth2/authorize')
  url.searchParams.set('client_id', clientId)
  url.searchParams.set(
    'redirect_uri',
    `${window.location.origin}/oauth/discord`
  )
  url.searchParams.set('response_type', 'code')
  url.searchParams.set('scope', 'identify+openid')
  url.searchParams.set('state', state)
  return url.toString()
}

/**
 * Build OIDC OAuth URL
 */
export function buildOIDCOAuthUrl(
  authUrl: string,
  clientId: string,
  state: string
): string {
  const url = new URL(authUrl)
  url.searchParams.set('client_id', clientId)
  url.searchParams.set('redirect_uri', `${window.location.origin}/oauth/oidc`)
  url.searchParams.set('response_type', 'code')
  url.searchParams.set('scope', 'openid profile email')
  url.searchParams.set('state', state)
  return url.toString()
}

/**
 * Build LinuxDO OAuth URL
 */
export function buildLinuxDOOAuthUrl(clientId: string, state: string): string {
  return `https://connect.linux.do/oauth2/authorize?response_type=code&client_id=${clientId}&state=${state}`
}

// ============================================================================
// OAuth Helper Functions
// ============================================================================

/**
 * Get OAuth state token
 * Includes affiliate code from localStorage if available
 */
export async function getOAuthState(): Promise<string | null> {
  try {
    let path = '/api/oauth/state'
    const affCode = localStorage.getItem('aff')
    if (affCode && affCode.length > 0) {
      path += `?aff=${affCode}`
    }
    const res = await api.get(path)
    if (res.data.success) {
      return res.data.data
    }
    return null
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to get OAuth state:', error)
    return null
  }
}

async function openOAuthBindPopup(
  provider: string,
  buildUrl: (state: string) => string
): Promise<void> {
  const popup = window.open('', '_blank')
  if (!popup) return

  const state = await getOAuthState()
  if (!state) {
    popup.close()
    return
  }
  if (!markOAuthBindPopup(getOAuthSessionStorage(popup), provider, state)) {
    popup.close()
    return
  }

  popup.location.href = buildUrl(state)
}

/**
 * Handle GitHub OAuth binding/login
 */
export async function handleGitHubOAuth(clientId: string): Promise<void> {
  await openOAuthBindPopup('github', (state) =>
    buildGitHubOAuthUrl(clientId, state)
  )
}

/**
 * Handle Discord OAuth binding/login
 */
export async function handleDiscordOAuth(clientId: string): Promise<void> {
  await openOAuthBindPopup('discord', (state) =>
    buildDiscordOAuthUrl(clientId, state)
  )
}

/**
 * Handle OIDC OAuth binding/login
 */
export async function handleOIDCOAuth(
  authUrl: string,
  clientId: string
): Promise<void> {
  await openOAuthBindPopup('oidc', (state) =>
    buildOIDCOAuthUrl(authUrl, clientId, state)
  )
}

/**
 * Handle LinuxDO OAuth binding/login
 */
export async function handleLinuxDOOAuth(clientId: string): Promise<void> {
  await openOAuthBindPopup('linuxdo', (state) =>
    buildLinuxDOOAuthUrl(clientId, state)
  )
}
