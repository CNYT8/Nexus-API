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
function isJsonObjectValue(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function parseOptionalJson(value: string | undefined): unknown {
  if (!value?.trim()) return undefined
  return JSON.parse(value)
}

function getStringAtPath(source: unknown, path: string[]): string {
  let current = source
  for (const key of path) {
    if (!isJsonObjectValue(current) || !(key in current)) {
      return ''
    }
    current = current[key]
  }
  return typeof current === 'string' ? current.trim() : ''
}

function firstStringAtPaths(source: unknown, paths: string[][]): string {
  for (const path of paths) {
    const value = getStringAtPath(source, path)
    if (value) return value
  }
  return ''
}

const codexAccessTokenPaths = [
  ['access_token'],
  ['accessToken'],
  ['token'],
  ['tokens', 'access_token'],
  ['tokens', 'accessToken'],
  ['credentials', 'access_token'],
  ['credentials', 'accessToken'],
  ['credentials', 'token'],
]

const codexAccountIDPaths = [
  ['account_id'],
  ['accountId'],
  ['chatgpt_account_id'],
  ['chatgptAccountId'],
  ['tokens', 'account_id'],
  ['tokens', 'accountId'],
  ['tokens', 'chatgpt_account_id'],
  ['tokens', 'chatgptAccountId'],
  ['account', 'id'],
  ['account', 'account_id'],
  ['account', 'accountId'],
  ['account', 'chatgpt_account_id'],
  ['account', 'chatgptAccountId'],
  ['credentials', 'account_id'],
  ['credentials', 'accountId'],
  ['credentials', 'chatgpt_account_id'],
  ['credentials', 'chatgptAccountId'],
]

function hasCodexTokenSignal(source: unknown): boolean {
  return isJsonObjectValue(source) && firstStringAtPaths(source, codexAccessTokenPaths) !== ''
}

function firstCodexAccountSource(accounts: unknown): Record<string, unknown> | null {
  if (!Array.isArray(accounts)) return null
  for (const account of accounts) {
    if (!isJsonObjectValue(account)) continue
    const platform = firstStringAtPaths(account, [['platform']]).toLowerCase()
    const accountType = firstStringAtPaths(account, [['type']]).toLowerCase()
    if (platform && platform !== 'openai') continue
    if (accountType && accountType !== 'oauth' && accountType !== 'codex') continue
    if (hasCodexTokenSignal(account)) return account
  }
  return null
}

function selectCodexKeySource(source: Record<string, unknown>): Record<string, unknown> {
  if (hasCodexTokenSignal(source)) return source
  const data = isJsonObjectValue(source.data) ? source.data : undefined
  return (
    firstCodexAccountSource(source.accounts) ||
    firstCodexAccountSource(source.Accounts) ||
    firstCodexAccountSource(data?.accounts) ||
    firstCodexAccountSource(data?.Accounts) ||
    source
  )
}

function collectCodexKeySources(source: unknown): Record<string, unknown>[] {
  if (!isJsonObjectValue(source)) return []
  if (hasCodexTokenSignal(source)) return [source]
  const values: unknown[] = []
  const appendAccounts = (accounts: unknown): void => {
    if (Array.isArray(accounts)) values.push(...accounts)
  }
  const data = isJsonObjectValue(source.data) ? source.data : undefined
  appendAccounts(source.accounts)
  appendAccounts(source.Accounts)
  appendAccounts(source.configs)
  appendAccounts(source.Configs)
  appendAccounts(source.items)
  appendAccounts(source.Items)
  appendAccounts(source.keys)
  appendAccounts(source.Keys)
  appendAccounts(data?.accounts)
  appendAccounts(data?.Accounts)
  appendAccounts(data?.configs)
  appendAccounts(data?.Configs)
  appendAccounts(data?.items)
  appendAccounts(data?.Items)
  appendAccounts(data?.keys)
  appendAccounts(data?.Keys)
  appendAccounts(source.data)

  const sources: Record<string, unknown>[] = []
  for (const value of values) {
    if (!isJsonObjectValue(value)) continue
    if (hasCodexTokenSignal(value)) {
      sources.push(value)
      continue
    }
    sources.push(...collectCodexKeySources(value))
  }
  return sources
}

function decodeCodexJWTClaims(token: string): Record<string, unknown> | null {
  const parts = token.trim().split('.')
  if (parts.length !== 3 || typeof atob !== 'function') return null
  try {
    const normalized = parts[1].replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '=')
    const claims = JSON.parse(atob(padded))
    return isJsonObjectValue(claims) ? claims : null
  } catch {
    return null
  }
}

function getCodexJWTAccountID(token: string): string {
  const claims = decodeCodexJWTClaims(token)
  const auth = claims?.['https://api.openai.com/auth']
  if (!isJsonObjectValue(auth)) return ''
  return typeof auth.chatgpt_account_id === 'string'
    ? auth.chatgpt_account_id.trim()
    : ''
}

export function isCodexCredential(value: string | undefined): boolean {
  try {
    const parsed = parseOptionalJson(value)
    if (parsed === undefined) return true
    if (!isJsonObjectValue(parsed)) return false
    const sources = collectCodexKeySources(parsed)
    const selectedSources = sources.length > 0 ? sources : [selectCodexKeySource(parsed)]
    return selectedSources.every((source) => {
      const accessToken = firstStringAtPaths(source, codexAccessTokenPaths)
      const accountID =
        firstStringAtPaths(source, codexAccountIDPaths) ||
        getCodexJWTAccountID(accessToken)
      return accessToken !== '' && accountID !== ''
    })
  } catch {
    return false
  }
}
