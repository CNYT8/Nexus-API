/*
Copyright (C) 2025 QuantumNous

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

import { AxiosError, CanceledError } from 'axios';
import { API } from '../../../../helpers';
import {
  buildAssertionResult,
  isPasskeySupported,
  prepareCredentialRequestOptions,
} from '../../../../helpers/passkey';
import { currentSecurityOwner } from './securityManagement';

async function securityRequest(config, signal, owner) {
  const assertOwner = () => {
    if (signal.aborted || !owner || currentSecurityOwner() !== owner) {
      throw new CanceledError('Security request owner changed');
    }
  };
  assertOwner();
  const transforms = API.defaults.transformRequest;
  const response = await API.request({
    ...config,
    signal,
    disableDuplicate: true,
    skipBusinessError: true,
    skipErrorHandler: true,
    validateStatus: () => true,
    transformRequest: [
      (data, headers) => {
        assertOwner();
        headers.set('New-API-User', String(owner));
        return data;
      },
      ...(Array.isArray(transforms)
        ? transforms
        : transforms
          ? [transforms]
          : []),
    ],
  });
  assertOwner();
  if (response.status < 200 || response.status >= 300) {
    throw new AxiosError(
      'Security request failed',
      undefined,
      response.config,
      response.request,
      response,
    );
  }
  return response.data;
}

export const getLoginDevices = async (signal, owner) =>
  securityRequest({ method: 'get', url: '/api/user/sessions' }, signal, owner);
export const revokeLoginDevice = async (id, signal, owner) =>
  securityRequest(
    { method: 'delete', url: `/api/user/sessions/${id}` },
    signal,
    owner,
  );
export const revokeOtherLoginDevices = async (signal, owner) =>
  securityRequest(
    { method: 'post', url: '/api/user/sessions/revoke-others', data: {} },
    signal,
    owner,
  );
export const getPersonalAccessTokens = async (signal, owner, page = 1, pageSize = 20) => {
  const response = await securityRequest(
    { method: 'get', url: '/api/user/access-tokens', params: { page, page_size: pageSize } },
    signal,
    owner,
  );
  const result = response.data;
  // Reject a legacy array response rather than inventing an empty page/limit.
  if (response.success && (!result || !Array.isArray(result.items) ||
    result.page !== page || result.page_size !== pageSize ||
    !Number.isSafeInteger(result.total) || result.total < 0 ||
    !Number.isSafeInteger(result.active_count) || result.active_count < 0 ||
    !Number.isSafeInteger(result.limit) || result.limit <= 0)) {
    throw new Error('Invalid personal access token page');
  }
  return response;
};
export const updatePersonalAccessToken = async (id, data, signal, owner) =>
  securityRequest(
    { method: 'put', url: `/api/user/access-tokens/${id}`, data },
    signal,
    owner,
  );
export const createPersonalAccessToken = async (data, signal, owner) =>
  securityRequest(
    { method: 'post', url: '/api/user/access-tokens', data },
    signal,
    owner,
  );
export const revokePersonalAccessToken = async (id, signal, owner) =>
  securityRequest(
    { method: 'delete', url: `/api/user/access-tokens/${id}` },
    signal,
    owner,
  );

export async function getSecurityVerificationMethods(signal, owner) {
  const [twoFA, passkey, supported] = await Promise.all([
    securityRequest(
      { method: 'get', url: '/api/user/2fa/status' },
      signal,
      owner,
    ),
    securityRequest({ method: 'get', url: '/api/user/passkey' }, signal, owner),
    isPasskeySupported(),
  ]);
  if (!twoFA.success || !passkey.success)
    throw new Error('Verification methods unavailable');
  return {
    has2FA: twoFA.data?.enabled === true,
    hasPasskey: passkey.data?.enabled === true,
    passkeySupported: supported,
  };
}

export async function verifySecurityAction(method, code, signal, owner) {
  if (method === 'passkey') {
    const begin = await securityRequest(
      { method: 'post', url: '/api/user/passkey/verify/begin' },
      signal,
      owner,
    );
    if (!begin.success) throw new Error('Verification failed');
    const credential = await navigator.credentials.get({
      publicKey: prepareCredentialRequestOptions(
        begin.data?.options ?? begin.data,
      ),
      signal,
    });
    if (!credential) throw new CanceledError('Passkey verification cancelled');
    const assertion = buildAssertionResult(credential);
    if (!assertion) throw new Error('Verification failed');
    const finish = await securityRequest(
      {
        method: 'post',
        url: '/api/user/passkey/verify/finish',
        data: assertion,
      },
      signal,
      owner,
    );
    if (!finish.success) throw new Error('Verification failed');
  } else if (method !== '2fa' || !code.trim()) {
    throw new Error('Verification code required');
  }
  const response = await securityRequest(
    {
      method: 'post',
      url: '/api/verify',
      data: { method, ...(method === '2fa' ? { code: code.trim() } : {}) },
    },
    signal,
    owner,
  );
  if (!response.success) throw new Error('Verification failed');
}
