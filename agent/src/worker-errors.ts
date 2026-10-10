/** Only the bridge transport boundary may label an error as retryable. */
export class RetryableBridgeError extends Error {
  constructor(readonly operation: string, readonly httpStatus?: number, readonly retryAfterMs?: number) {
    super(`Canvas bridge temporarily unavailable on ${operation}${httpStatus ? ` (HTTP ${httpStatus})` : ''}`);
    this.name = 'RetryableBridgeError';
  }
}

export function safeWorkerDetail(value: string): string {
  return value.replace(/https?:\/\/\S+/gi, '[url]')
    .replace(/Bearer\s+\S+/gi, 'Bearer [redacted]')
    .replace(/(api[_-]?key|token|password|secret|cookie)\s*[:=]\s*[^\s,;]+/gi, '$1=[redacted]')
    .replace(/[\r\n\t]+/g, ' ').slice(0, 240);
}
