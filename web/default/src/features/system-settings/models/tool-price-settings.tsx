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
import { memo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { Code2, Copy, Eye, Plus, Trash2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { useUpdateOption } from '../hooks/use-update-option'
import { normalizeSettingValue, useResetForm } from '../hooks/use-reset-form'

const OPTION_KEY = 'tool_price_setting.prices'

const DEFAULT_PRICES: Record<string, number> = {
  web_search: 10.0,
  web_search_preview: 10.0,
  'web_search_preview:gpt-4o*': 25.0,
  'web_search_preview:gpt-4.1*': 25.0,
  'web_search_preview:gpt-4o-mini*': 25.0,
  'web_search_preview:gpt-4.1-mini*': 25.0,
  file_search: 2.5,
  google_search: 14.0,
}

type ToolPriceRow = {
  id: number
  key: string
  price: string | number
}

function rowsToObject(rows: ToolPriceRow[]): Record<string, number> | null {
  const prices: Record<string, number> = {}
  for (const row of rows) {
    const k = row.key.trim()
    const price = Number(row.price)
    if (
      !['number', 'string'].includes(typeof row.price) ||
      String(row.price).trim() === '' ||
      !Number.isFinite(price) ||
      price < 0
    ) {
      return null
    }
    if (!k || Object.hasOwn(prices, k)) return null
    Object.defineProperty(prices, k, { value: price, enumerable: true })
  }
  return prices
}

function objectToRows(prices: Record<string, number>): ToolPriceRow[] {
  return Object.entries(prices).map(([key, price], index) => ({
    id: index + 1,
    key,
    price,
  }))
}

const pricesSchema = z.record(z.string(), z.number().finite().nonnegative())
const editorSchema = z.object({
  rows: z.array(z.object({
    id: z.number(),
    key: z.string(),
    price: z.union([z.string(), z.number()]),
  })),
  jsonText: z.string(),
})

function parsePrices(text: string): Record<string, number> | null {
  try {
    const parsed = pricesSchema.safeParse(JSON.parse(text) as unknown)
    return parsed.success && rowsToObject(objectToRows(parsed.data)) !== null
      ? parsed.data
      : null
  } catch {
    return null
  }
}

function createEditor(raw: string): string {
  const jsonText = raw || JSON.stringify(DEFAULT_PRICES, null, 2)
  const prices = parsePrices(jsonText)
  return JSON.stringify({ rows: prices ? objectToRows(prices) : [], jsonText })
}

function readEditor(value: string) {
  return editorSchema.parse(JSON.parse(value) as unknown)
}

function normalizeEditor(value: unknown): unknown {
  if (typeof value !== 'string') return value
  const editor = readEditor(value)
  const prices = rowsToObject(editor.rows)
  return prices !== null && parsePrices(editor.jsonText) !== null
    ? normalizeSettingValue(prices)
    : value
}

type ToolPriceSettingsProps = {
  defaultValue: string
}

export const ToolPriceSettings = memo(function ToolPriceSettings({
  defaultValue,
}: ToolPriceSettingsProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const defaults = { editor: createEditor(defaultValue) }
  const form = useForm({ defaultValues: defaults })
  const submitForm = useResetForm(form, defaults, normalizeEditor)
  const { rows, jsonText } = readEditor(form.watch('editor'))
  const [editMode, setEditMode] = useState<'visual' | 'json'>(
    parsePrices(jsonText) === null ? 'json' : 'visual'
  )
  const currentPrices = rowsToObject(rows)
  const jsonError = parsePrices(jsonText) === null
    ? t('Invalid tool prices. Use unique, non-empty tool names and finite, non-negative numbers.')
    : ''
  const showJson = editMode === 'json' || !!jsonError

  const syncFromRows = (nextRows: ToolPriceRow[]) => {
    const prices = rowsToObject(nextRows)
    form.setValue('editor', JSON.stringify({
      rows: nextRows,
      jsonText: prices === null ? jsonText : JSON.stringify(prices, null, 2),
    }), { shouldDirty: true })
  }
  const handleJsonChange = (text: string) => {
    const prices = parsePrices(text)
    form.setValue('editor', JSON.stringify({
      rows: prices === null ? rows : objectToRows(prices),
      jsonText: text,
    }), { shouldDirty: true })
  }
  const updateRow = (id: number, field: 'key' | 'price', value: string | number) => {
    syncFromRows(rows.map((row) => row.id === id ? { ...row, [field]: value } : row))
  }
  const addRow = () => {
    const id = Math.max(0, ...rows.map((row) => row.id)) + 1
    syncFromRows([...rows, { id, key: '', price: '' }])
  }
  const removeRow = (id: number) => syncFromRows(rows.filter((row) => row.id !== id))
  const resetToDefault = () => {
    form.setValue('editor', createEditor(''), { shouldDirty: true })
  }
  const handleCopyJson = async () => {
    try {
      await navigator.clipboard.writeText(jsonText)
      toast.success(t('Copied to clipboard'))
    } catch {
      toast.error(t('Failed to copy'))
    }
  }
  const handleSave = submitForm(async (values) => {
    const editor = readEditor(values.editor)
    const prices = rowsToObject(editor.rows)
    if (prices === null || parsePrices(editor.jsonText) === null) {
      toast.error(t('Invalid tool prices. Use unique, non-empty tool names and finite, non-negative numbers.'))
      return
    }
    await updateOption.saveOptions([{ key: OPTION_KEY, value: JSON.stringify(prices) }])
  })
  const toggleEditMode = () => {
    if (jsonError || currentPrices === null) return
    setEditMode((previous) => previous === 'visual' ? 'json' : 'visual')
  }

  return (
    <div className='space-y-4'>
      <Alert>
        <AlertDescription className='space-y-1 text-sm'>
          <div>
            {t(
              'Configure per-tool unit prices ($/1K calls). Per-request models do not incur additional tool fees.'
            )}
          </div>
          <div>
            <span className='font-medium'>{t('Format')}:</span>{' '}
            <code className='bg-muted rounded px-1 py-0.5 text-xs'>
              web_search_preview
            </code>{' '}
            {t('is the default price; ')}
            <code className='bg-muted rounded px-1 py-0.5 text-xs'>
              web_search_preview:gpt-4o*
            </code>{' '}
            {t('overrides for matching model prefix.')}
          </div>
        </AlertDescription>
      </Alert>

      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex flex-wrap items-center gap-2'>
          {!showJson ? (
            <>
              <Button variant='outline' size='sm' onClick={addRow}>
                <Plus className='mr-2 h-4 w-4' />
                {t('Add')}
              </Button>
              <Button variant='ghost' size='sm' onClick={resetToDefault}>
                {t('Restore defaults')}
              </Button>
            </>
          ) : (
            <>
              <Button variant='ghost' size='sm' onClick={handleCopyJson}>
                <Copy className='mr-2 h-4 w-4' />
                {t('Copy')}
              </Button>
              <Button variant='ghost' size='sm' onClick={resetToDefault}>
                {t('Restore defaults')}
              </Button>
            </>
          )}
        </div>
        <Button variant='outline' size='sm' onClick={toggleEditMode} disabled={!!jsonError || currentPrices === null}>
          {!showJson ? (
            <>
              <Code2 className='mr-2 h-4 w-4' />
              {t('Switch to JSON')}
            </>
          ) : (
            <>
              <Eye className='mr-2 h-4 w-4' />
              {t('Switch to Visual')}
            </>
          )}
        </Button>
      </div>

      {!showJson ? (
        <div className='overflow-hidden rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Tool identifier')}</TableHead>
                <TableHead className='w-[200px]'>
                  {t('Price ($/1K calls)')}
                </TableHead>
                <TableHead className='w-[80px] text-right'>
                  {t('Actions')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={3}
                    className='text-muted-foreground py-8 text-center'
                  >
                    {t('No tools configured')}
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((row) => (
                  <TableRow key={row.id}>
                    <TableCell>
                      <Input
                        value={row.key}
                        placeholder='web_search_preview:gpt-4o*'
                        onChange={(e) =>
                          updateRow(row.id, 'key', e.target.value)
                        }
                      />
                    </TableCell>
                    <TableCell>
                      <Input
                        inputMode='decimal'
                        value={row.price}
                        onChange={(e) =>
                          updateRow(
                            row.id,
                            'price',
                            e.target.value
                          )
                        }
                      />
                    </TableCell>
                    <TableCell className='text-right'>
                      <Button
                        variant='ghost'
                        size='icon'
                        onClick={() => removeRow(row.id)}
                        aria-label={t('Delete')}
                      >
                        <Trash2 className='text-destructive h-4 w-4' />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      ) : (
        <div className='space-y-2'>
          <Textarea
            value={jsonText}
            onChange={(e) => handleJsonChange(e.target.value)}
            className='font-mono text-sm'
            rows={12}
            spellCheck={false}
          />
          {jsonError && <p className='text-destructive text-sm'>{jsonError}</p>}
        </div>
      )}

      <p className={currentPrices === null ? 'text-destructive text-sm' : 'text-muted-foreground text-sm'}>
        {t('Prices must be finite, non-negative numbers. Enter 0 for free.')}
      </p>
      <div className='flex justify-end'>
        <Button
          onClick={handleSave}
          disabled={
            form.formState.isSubmitting || updateOption.isPending || !!jsonError || currentPrices === null
          }
        >
          {t('Save tool prices')}
        </Button>
      </div>
    </div>
  )
})
