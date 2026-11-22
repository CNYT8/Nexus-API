import { api } from '@/lib/api'
import {
  prepareLoginPassword,
  type PasswordEncryptionKeyResponse,
} from '../../../../../shared/password-encryption'

export { PasswordEncryptionError } from '../../../../../shared/password-encryption'

export async function preparePasswordLogin(password: string) {
  return prepareLoginPassword(password, async () => {
    const response = await api.get<PasswordEncryptionKeyResponse>(
      '/api/user/login/encryption-key',
      {
        disableDuplicate: true,
        skipBusinessError: true,
        skipErrorHandler: true,
      }
    )
    return response.data
  })
}
