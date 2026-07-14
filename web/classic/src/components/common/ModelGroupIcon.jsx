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

import React from 'react';
import { Claude, DeepSeek, Gemini, OpenAI } from '@lobehub/icons';

const MODEL_GROUP_ICON_RULES = [
  {
    keywords: ['claude'],
    render: (size) => <Claude.Color size={size} />,
  },
  {
    keywords: ['gpt'],
    render: (size) => <OpenAI size={size} />,
  },
  {
    keywords: ['gemini'],
    render: (size) => <Gemini.Color size={size} />,
  },
  {
    keywords: ['deepseek'],
    render: (size) => <DeepSeek.Color size={size} />,
  },
];

export function getModelGroupIcon(groupName, size = 14) {
  const normalized = String(groupName || '').trim().toLowerCase();
  if (!normalized || normalized === 'all' || normalized === 'auto') {
    return null;
  }

  const matchedRule = MODEL_GROUP_ICON_RULES.find((rule) =>
    rule.keywords.some((keyword) => normalized.includes(keyword)),
  );

  return matchedRule ? matchedRule.render(size) : null;
}
