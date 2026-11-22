import { API } from './api';
import { prepareLoginPassword } from '../../../shared/password-encryption';

export async function preparePasswordLogin(password) {
  return prepareLoginPassword(password, async () => {
    const response = await API.get('/api/user/login/encryption-key', {
      disableDuplicate: true,
      skipErrorHandler: true,
    });
    return response.data;
  });
}
