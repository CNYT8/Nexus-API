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

export const WEB_RISK_REQUIRED_EVENT = 'nexus:web-risk-required';

export function isWebRiskVerificationRequired(error) {
  return (
    error?.response?.status === 403 &&
    error?.response?.data?.code === 'WEB_RISK_VERIFICATION_REQUIRED'
  );
}

export function dispatchWebRiskVerificationRequired(siteKey = '') {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(
    new CustomEvent(WEB_RISK_REQUIRED_EVENT, {
      detail: { siteKey },
    }),
  );
}
