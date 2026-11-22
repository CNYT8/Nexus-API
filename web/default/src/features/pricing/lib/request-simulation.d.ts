export type SimulationInput = {
  expression: string
  rules?: string
  bodyText?: string
  headersText?: string
  timeText?: string
  variablesText?: string
}

export type SimulationResult =
  | {
      ok: true
      baseCost: number
      multiplier: number
      cost: number
      tier: string
      time: string
    }
  | { ok: false; error: string }

export function parseSimulationTime(text: string, now?: Date): Date
export function simulateRequest(input: SimulationInput): SimulationResult
