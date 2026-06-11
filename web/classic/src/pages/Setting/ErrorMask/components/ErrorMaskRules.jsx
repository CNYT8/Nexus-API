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
import React, { useCallback, useState } from 'react';
import {
  Button,
  Input,
  InputNumber,
  Popconfirm,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

let _idCounter = 0;
const uid = () => `em_${++_idCounter}`;

function safeParse(str) {
  if (!str || !str.trim()) return [];
  try {
    const data = JSON.parse(str);
    if (!Array.isArray(data)) return [];
    return data;
  } catch {
    return [];
  }
}

export function parseErrorMaskRules(jsonStr) {
  return safeParse(jsonStr).map((r) => ({
    _id: uid(),
    status: typeof r.status === 'number' ? r.status : 0,
    pattern: typeof r.pattern === 'string' ? r.pattern : '',
    replacement: typeof r.replacement === 'string' ? r.replacement : '',
  }));
}

export function serializeErrorMaskRules(rules) {
  const cleaned = rules
    .map((r) => ({
      status: Number.isFinite(r.status) ? r.status : 0,
      pattern: r.pattern || '',
      replacement: r.replacement || '',
    }))
    .filter((r) => r.replacement.trim() !== '');
  if (cleaned.length === 0) return '';
  return JSON.stringify(cleaned);
}

export default function ErrorMaskRules({ value, onChange }) {
  const { t } = useTranslation();
  const [rules, setRules] = useState(() => parseErrorMaskRules(value));

  const emit = useCallback(
    (newRules) => {
      setRules(newRules);
      onChange?.(serializeErrorMaskRules(newRules));
    },
    [onChange],
  );

  const updateRule = useCallback(
    (id, field, val) => {
      emit(
        rules.map((r) => (r._id === id ? { ...r, [field]: val } : r)),
      );
    },
    [rules, emit],
  );

  const removeRule = useCallback(
    (id) => emit(rules.filter((r) => r._id !== id)),
    [rules, emit],
  );

  const addRule = useCallback(() => {
    emit([
      ...rules,
      { _id: uid(), status: 0, pattern: '', replacement: '' },
    ]);
  }, [rules, emit]);

  return (
    <div className='space-y-2'>
      {rules.length === 0 ? (
        <Text type='tertiary' className='block text-center py-4'>
          {t('暂无规则，点击下方按钮添加')}
        </Text>
      ) : (
        rules.map((rule) => (
          <div
            key={rule._id}
            className='flex items-center gap-2'
            style={{ marginBottom: 6 }}
          >
            <InputNumber
              size='small'
              value={rule.status || undefined}
              min={0}
              max={599}
              placeholder={t('状态码')}
              onChange={(v) => updateRule(rule._id, 'status', v || 0)}
              style={{ width: 100 }}
            />
            <Input
              size='small'
              value={rule.pattern}
              placeholder={t('匹配文本（含此子串即命中，留空表示匹配任意）')}
              onChange={(v) => updateRule(rule._id, 'pattern', v)}
              style={{ flex: 1 }}
            />
            <Input
              size='small'
              value={rule.replacement}
              placeholder={t('替换文案，支持占位符 {message} {code} {status} ...')}
              onChange={(v) => updateRule(rule._id, 'replacement', v)}
              style={{ flex: 2 }}
            />
            <Popconfirm
              title={t('确认删除该规则？')}
              onConfirm={() => removeRule(rule._id)}
              position='left'
            >
              <Button
                icon={<IconDelete />}
                type='danger'
                theme='borderless'
                size='small'
              />
            </Popconfirm>
          </div>
        ))
      )}
      <div className='mt-3 flex justify-center'>
        <Button icon={<IconPlus />} theme='outline' onClick={addRule}>
          {t('添加规则')}
        </Button>
      </div>
    </div>
  );
}
