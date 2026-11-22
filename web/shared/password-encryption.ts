export interface PasswordEncryptionKeyResponse {
  success?: boolean
  data?: {
    enabled?: boolean
    kid?: string
    public_key?: string
  }
}

export type LoginPasswordFields =
  | { password: string }
  | { password_encrypted: string; encryption_key_id: string }

// Callers translate this local failure using their existing login error text.
export class PasswordEncryptionError extends Error {
  constructor() {
    super('Password encryption failed')
    this.name = 'PasswordEncryptionError'
  }
}

function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary)
}

function publicKeyBytes(pem: string): Uint8Array<ArrayBuffer> {
  if (
    pem.length > 8192 ||
    !pem.startsWith('-----BEGIN PUBLIC KEY-----') ||
    !pem.trimEnd().endsWith('-----END PUBLIC KEY-----')
  ) {
    throw new PasswordEncryptionError()
  }
  const body = pem
    .replace('-----BEGIN PUBLIC KEY-----', '')
    .replace('-----END PUBLIC KEY-----', '')
    .replace(/\s/g, '')
  return Uint8Array.from(atob(body), (char) => char.charCodeAt(0))
}

// Fetch on every submission: a cached disabled flag must never cause a later
// enabled deployment to receive plaintext. Network/key/crypto errors propagate.
export async function prepareLoginPassword(
  password: string,
  fetchKey: () => Promise<PasswordEncryptionKeyResponse>
): Promise<LoginPasswordFields> {
  try {
    const response = await fetchKey()
    if (response?.success !== true || !response.data) {
      throw new PasswordEncryptionError()
    }
    if (response.data.enabled === false) return { password }
    const { enabled, kid, public_key: publicKeyPEM } = response.data
    if (
      enabled !== true ||
      typeof kid !== 'string' ||
      !/^[a-f0-9]{32}$/.test(kid) ||
      typeof publicKeyPEM !== 'string'
    ) {
      throw new PasswordEncryptionError()
    }

    // Native Web Crypto is available on HTTPS and localhost. Unsupported
    // contexts fail closed; encryption never falls back to plaintext.
    const crypto = globalThis.crypto
    if (!crypto?.subtle) throw new PasswordEncryptionError()
    const encoder = new TextEncoder()
    const plaintext = encoder.encode(password)
    if (plaintext.length === 0 || plaintext.length > 512) {
      throw new PasswordEncryptionError()
    }
    const der = publicKeyBytes(publicKeyPEM)
    const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', der))
    const actualKeyID = Array.from(digest.subarray(0, 16), (byte) =>
      byte.toString(16).padStart(2, '0')
    ).join('')
    if (actualKeyID !== kid) throw new PasswordEncryptionError()
    const publicKey = await crypto.subtle.importKey(
      'spki',
      der,
      { name: 'RSA-OAEP', hash: 'SHA-256' },
      false,
      ['encrypt']
    )
    if ((publicKey.algorithm as RsaHashedKeyAlgorithm).modulusLength < 2048) {
      throw new PasswordEncryptionError()
    }

    const rawKey = crypto.getRandomValues(new Uint8Array(32))
    const nonce = crypto.getRandomValues(new Uint8Array(12))
    const aesKey = await crypto.subtle.importKey(
      'raw',
      rawKey,
      'AES-GCM',
      false,
      ['encrypt']
    )
    const ciphertext = await crypto.subtle.encrypt(
      {
        name: 'AES-GCM',
        iv: nonce,
        additionalData: encoder.encode(`password-v2:${kid}`),
        tagLength: 128,
      },
      aesKey,
      plaintext
    )
    const wrappedKey = await crypto.subtle.encrypt(
      { name: 'RSA-OAEP', label: encoder.encode('password-v2') },
      publicKey,
      rawKey
    )
    const payload = [
      'v2',
      toBase64(new Uint8Array(wrappedKey)),
      toBase64(nonce),
      toBase64(new Uint8Array(ciphertext)),
    ].join('.')
    if (payload.length > 4096) throw new PasswordEncryptionError()
    return { password_encrypted: payload, encryption_key_id: kid }
  } catch {
    throw new PasswordEncryptionError()
  }
}
