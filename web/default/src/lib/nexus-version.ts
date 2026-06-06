export const NEXUS_API_RELEASES_URL = 'https://github.com/CNYT8/Nexus-API/releases'
export const NEXUS_API_LATEST_RELEASE_API =
  'https://api.github.com/repos/CNYT8/Nexus-API/releases/latest'
export const NEW_API_RELEASES_URL = 'https://github.com/QuantumNous/new-api/releases'
export const NEW_API_LATEST_RELEASE_API =
  'https://api.github.com/repos/QuantumNous/new-api/releases/latest'

export type ReleaseInfo = {
  tag_name: string
  name?: string
  body?: string
  html_url?: string
  published_at?: string
}

export async function fetchLatestRelease(apiUrl: string): Promise<ReleaseInfo> {
  const response = await fetch(apiUrl, {
    headers: {
      Accept: 'application/vnd.github+json',
      'User-Agent': 'nexus-api-dashboard',
    },
  })

  if (!response.ok) {
    throw new Error('Failed to contact GitHub releases API')
  }

  const data = (await response.json()) as ReleaseInfo
  if (!data?.tag_name) {
    throw new Error('Unexpected release payload')
  }

  return data
}
