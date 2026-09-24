import { REQUEST_CONFIG } from '@/constants/request'

interface PendingRequest<T> {
  promise: Promise<T>
  controller: AbortController
}

interface QueuedRequest<T> {
  key: string
  controller: AbortController
  task: (signal: AbortSignal) => Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
  timeout: number
  retryAttempts: number
  shouldRetry: (error: unknown) => boolean
}

export interface RequestManagerOptions {
  timeout?: number
  retryAttempts?: number
  shouldRetry?: (error: unknown) => boolean
}

function createAbortError(message = 'Request aborted'): Error {
  const error = new Error(message)
  error.name = 'AbortError'
  return error
}

function waitForAbort(signal: AbortSignal): Promise<never> {
  if (signal.aborted)
    return Promise.reject(createAbortError())

  return new Promise((_, reject) => {
    const abort = () => reject(createAbortError())
    signal.addEventListener('abort', abort, { once: true })
  })
}

export class RequestManager {
  private readonly pending = new Map<string, PendingRequest<unknown>>()
  private readonly queue: Array<QueuedRequest<unknown>> = []
  private activeCount = 0

  run<T>(key: string, task: (signal: AbortSignal) => Promise<T>, options: RequestManagerOptions = {}): Promise<T> {
    const existing = this.pending.get(key) as PendingRequest<T> | undefined
    if (existing)
      return existing.promise

    const controller = new AbortController()
    const timeout = options.timeout ?? REQUEST_CONFIG.timeout.default
    const retryAttempts = options.retryAttempts ?? REQUEST_CONFIG.retry.attempts
    const shouldRetry = options.shouldRetry ?? (() => true)

    const promise = new Promise<T>((resolve, reject) => {
      this.queue.push({
        key,
        controller,
        task,
        resolve,
        reject,
        timeout,
        retryAttempts,
        shouldRetry,
      } as QueuedRequest<unknown>)
      this.drainQueue()
    }).finally(() => {
      this.pending.delete(key)
    })

    this.pending.set(key, { promise, controller })
    return promise
  }

  abort(key: string): void {
    const pending = this.pending.get(key)
    if (!pending)
      return

    pending.controller.abort()
    this.pending.delete(key)

    const index = this.queue.findIndex(request => request.key === key)
    if (index < 0)
      return

    const [request] = this.queue.splice(index, 1)
    request?.reject(createAbortError())
  }

  abortAll(): void {
    for (const request of this.pending.values())
      request.controller.abort()

    for (const request of this.queue)
      request.reject(createAbortError())

    this.pending.clear()
    this.queue.length = 0
  }

  private drainQueue(): void {
    while (this.activeCount < REQUEST_CONFIG.pool.maxConcurrent && this.queue.length > 0) {
      const request = this.queue.shift()
      if (!request)
        return

      if (request.controller.signal.aborted) {
        request.reject(createAbortError())
        continue
      }

      this.startActiveRequest(request)
    }
  }

  private startActiveRequest<T>(request: QueuedRequest<T>): void {
    this.activeCount += 1
    void (async () => {
      try {
        await this.execute(request)
      }
      finally {
        this.activeCount = Math.max(0, this.activeCount - 1)
        this.drainQueue()
      }
    })()
  }

  private async execute<T>(request: QueuedRequest<T>): Promise<void> {
    try {
      const result = await this.runWithTimeoutAndRetry(request)
      request.resolve(result)
    }
    catch (error) {
      request.reject(error)
    }
  }

  private async runWithTimeoutAndRetry<T>(request: QueuedRequest<T>): Promise<T> {
    let lastError: unknown

    for (let attempt = 0; attempt <= request.retryAttempts; attempt++) {
      if (request.controller.signal.aborted)
        throw createAbortError()

      const attemptController = new AbortController()
      const abortAttempt = () => attemptController.abort()
      const timeoutId = setTimeout(() => attemptController.abort(createAbortError('Request timeout')), request.timeout)
      request.controller.signal.addEventListener('abort', abortAttempt, { once: true })

      try {
        return await Promise.race([
          request.task(attemptController.signal),
          waitForAbort(attemptController.signal),
        ])
      }
      catch (error) {
        lastError = error
        if (request.controller.signal.aborted)
          throw error
        if (attempt >= request.retryAttempts || !request.shouldRetry(error))
          throw error
      }
      finally {
        clearTimeout(timeoutId)
        request.controller.signal.removeEventListener('abort', abortAttempt)
      }
    }

    throw lastError ?? new Error('Request failed')
  }
}

export const requestManager = new RequestManager()
