export type ResponseModelObservation = {
  requested_model: string
  upstream_model: string
  returned_model: string
}

export function readResponseModel(other: unknown, isAdmin: boolean): ResponseModelObservation | undefined {
  if (!isAdmin) return undefined
  try {
    const data = typeof other === 'string' ? JSON.parse(other) : other
    if (!data || typeof data !== 'object') return undefined
    const observation = (data as Record<string, unknown>).response_model
    if (!observation || typeof observation !== 'object' || Array.isArray(observation)) return undefined
    const fields = observation as Record<string, unknown>
    const text = (value: unknown) => typeof value === 'string' ? value.trim() : ''
    const result = {
      requested_model: text(fields.requested_model),
      upstream_model: text(fields.upstream_model),
      returned_model: text(fields.returned_model),
    }
    return result.returned_model ? result : undefined
  } catch {
    return undefined
  }
}

export function isResponseModelMismatch(observation: ResponseModelObservation | undefined): boolean {
  if (!observation?.returned_model) return false
  const returned = observation.returned_model.toLowerCase()
  const expected = [observation.requested_model, observation.upstream_model].filter(Boolean)
  return expected.length > 0 && !expected.some((model) => {
    const name = model.toLowerCase()
    return returned.startsWith(name) || returned.endsWith(name)
  })
}
