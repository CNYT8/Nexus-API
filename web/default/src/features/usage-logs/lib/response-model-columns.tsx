import { flexRender, type ColumnDef } from '@tanstack/react-table'
import { ResponseModelObservation } from '../components/response-model-observation'
import type { UsageLog } from '../types'

export function withResponseModelObservation(columns: ColumnDef<UsageLog>[], isAdmin: boolean): ColumnDef<UsageLog>[] {
  if (!isAdmin) return columns
  return columns.map((column) => {
    if (!('accessorKey' in column) || column.accessorKey !== 'model_name') return column
    return {
      ...column,
      cell: (context) => (
        <div className='flex flex-wrap items-center gap-1.5'>
          {flexRender(column.cell, context)}
          <ResponseModelObservation other={context.row.original.other} isAdmin={isAdmin} />
        </div>
      ),
    }
  })
}
