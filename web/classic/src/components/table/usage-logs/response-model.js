export function readResponseModel(other, isAdmin) {
  if (!isAdmin) return undefined;
  try {
    const data = typeof other === 'string' ? JSON.parse(other) : other;
    const observation = data?.response_model;
    if (!observation || typeof observation !== 'object' || Array.isArray(observation)) return undefined;
    const text = (value) => typeof value === 'string' ? value.trim() : '';
    const result = {
      requested_model: text(observation.requested_model),
      upstream_model: text(observation.upstream_model),
      returned_model: text(observation.returned_model),
    };
    return result.returned_model ? result : undefined;
  } catch {
    return undefined;
  }
}

export function isResponseModelMismatch(observation) {
  if (!observation?.returned_model) return false;
  const returned = observation.returned_model.toLowerCase();
  const expected = [observation.requested_model, observation.upstream_model].filter(Boolean);
  return expected.length > 0 && !expected.some((model) => {
    const name = model.toLowerCase();
    return returned.startsWith(name) || returned.endsWith(name);
  });
}
