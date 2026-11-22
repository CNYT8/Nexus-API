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
import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type {
  DefaultValues,
  FieldValues,
  Path,
  PathValue,
  UseFormReturn,
} from 'react-hook-form'

type NormalizeValue = (value: unknown, path: string) => unknown

// JSON editors may serialize the same saved value with different indentation.
export function normalizeSettingValue(value: unknown): unknown {
  if (typeof value === 'string') {
    const trimmed = value.trimStart()
    if (!trimmed.startsWith('[') && !trimmed.startsWith('{')) return value
    try {
      return normalizeSettingValue(JSON.parse(value) as unknown)
    } catch {
      return value
    }
  }
  if (Array.isArray(value)) return value.map(normalizeSettingValue)
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value)
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([key, item]) => [key, normalizeSettingValue(item)])
    )
  }
  return value
}

// Rebase on a confirmed server snapshot, then reapply editable dirty leaves.
// Unlike keepDirtyValues, this also clears dirtiness when a saved value matches.
export function resetFormPreservingDirtyValues<
  TFieldValues extends FieldValues,
  TContext,
  TTransformedValues,
>(
  form: UseFormReturn<TFieldValues, TContext, TTransformedValues>,
  values: DefaultValues<TFieldValues>,
  submitted?: TFieldValues,
  normalize: NormalizeValue = normalizeSettingValue
) {
  const dirtyValues = new Map<string, unknown>()
  const editedPaths = new Set<string>()
  const collect = (fields: object, prefix = '') => {
    Object.entries(fields).forEach(([key, dirty]) => {
      const path = prefix ? `${prefix}.${key}` : key
      if (dirty === true || Array.isArray(dirty)) {
        dirtyValues.set(path, form.getValues(path as Path<TFieldValues>))
      } else if (dirty && typeof dirty === 'object') {
        collect(dirty, path)
      }
    })
  }
  collect(form.formState.dirtyFields)
  if (submitted) {
    const collectEdits = (
      current: FieldValues,
      snapshot: FieldValues,
      prefix = ''
    ) => {
      Object.entries(current).forEach(([key, value]) => {
        const path = prefix ? `${prefix}.${key}` : key
        if (value && typeof value === 'object' && !Array.isArray(value)) {
          collectEdits(value, snapshot?.[key] ?? {}, path)
        } else if (JSON.stringify(value) !== JSON.stringify(snapshot?.[key])) {
          dirtyValues.set(path, value)
          editedPaths.add(path)
        }
      })
    }
    collectEdits(form.getValues(), submitted)
  }
  form.reset(values)
  dirtyValues.forEach((value, path) => {
    const serverValue = form.getValues(path as Path<TFieldValues>)
    const normalizedValue = normalize(value, path)
    const normalizedServer = normalize(serverValue, path)
    // Never normalize away edits made after the submitted snapshot.
    if (!editedPaths.has(path) && (
      JSON.stringify(normalizedValue) === JSON.stringify(normalizedServer) ||
      (typeof normalizedServer === 'number' && typeof normalizedValue === 'string' &&
        normalizedValue.trim() !== '' && Number(normalizedValue) === normalizedServer)
    )) return
    form.setValue(
      path as Path<TFieldValues>,
      value as PathValue<TFieldValues, Path<TFieldValues>>,
      { shouldDirty: true }
    )
  })

  if (!submitted || !editedPaths.size) return undefined
  // Props may still describe the previous query snapshot. Carry only real
  // edits forward; values normalized by this reset are not new user edits.
  const pendingSubmission = structuredClone(form.getValues())
  const carryEdits = (current: FieldValues, snapshot: FieldValues, prefix = '') => {
    Object.entries(current).forEach(([key, value]) => {
      const path = prefix ? `${prefix}.${key}` : key
      if (editedPaths.has(path)) {
        current[key] = snapshot?.[key]
      } else if (value && typeof value === 'object' && !Array.isArray(value)) {
        carryEdits(value, snapshot?.[key] ?? {}, path)
      }
    })
  }
  carryEdits(pendingSubmission, submitted)
  return pendingSubmission
}

/** Reconcile refreshed defaults without discarding unsaved edits. */
export function useResetForm<
  TFieldValues extends FieldValues,
  TContext,
  TTransformedValues,
>(
  form: UseFormReturn<TFieldValues, TContext, TTransformedValues>,
  values: DefaultValues<TFieldValues> | undefined,
  normalize: NormalizeValue = normalizeSettingValue
) {
  const queryClient = useQueryClient()
  const lastSerializedDefaults = useRef<string | null>(null)
  const submittedRef = useRef<TFieldValues | undefined>(undefined)
  const submittedVersionRef = useRef<number | undefined>(undefined)
  const submittingRef = useRef(false)
  const { dirtyFields, isSubmitting } = form.formState

  // Capture before validation starts, not in an effect after an edit may occur.
  const handleSubmit: typeof form.handleSubmit = (onValid, onInvalid) => {
    const submit = form.handleSubmit(onValid, onInvalid)
    return async (event) => {
      if (submittingRef.current) return
      submittingRef.current = true
      submittedRef.current = structuredClone(form.getValues())
      submittedVersionRef.current = queryClient.getQueryState(['system-options'])?.dataUpdateCount
      try {
        await submit(event)
      } finally {
        submittingRef.current = false
      }
    }
  }

  useEffect(() => {
    if (isSubmitting || submittingRef.current) return
    const submitted = submittedRef.current
    if (!values) return

    const serializedDefaults = JSON.stringify(values)
    const version = queryClient.getQueryState(['system-options'])?.dataUpdateCount
    const refreshedAfterSubmit = submitted !== undefined &&
      version !== submittedVersionRef.current
    const defaultsChanged = serializedDefaults !== lastSerializedDefaults.current
    if (defaultsChanged || refreshedAfterSubmit) {
      // A successful read may contain the same defaults after normalization.
      // A failed read must never make that unchanged baseline clear a draft.
      const pendingSubmission = resetFormPreservingDirtyValues(form, values, submitted, normalize)
      lastSerializedDefaults.current = serializedDefaults
      // Query notifications can deliver new props after isSubmitting becomes
      // false. Keep edits (including a return to the old baseline) until then.
      submittedRef.current = defaultsChanged ? undefined : pendingSubmission
      submittedVersionRef.current = submittedRef.current ? version : undefined
    }
  }, [values, form, dirtyFields, isSubmitting, normalize, queryClient])

  return handleSubmit
}
