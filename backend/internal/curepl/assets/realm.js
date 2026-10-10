(() => {
    const hostSend = globalThis["__go_cu_bridge"]
    delete globalThis["__go_cu_bridge"]
    const pending = new Map()
    let operations = []
    let requestCounter = 0
    const saved = new Map()
    const previous = new Map()
    const reached = new Set()
    let candidate = []
    const timers = new Map()
    let timerCounter = 0
    const scheduleTimer = (callback, delay, args, interval) => {
      if (typeof callback !== 'function') throw new TypeError('Timer callback must be a function')
      delay = Number(delay)
      // Match Node's asynchronous minimum and integer delay normalization,
      // without exposing Node functions or its Timeout objects to the guest.
      delay = !Number.isFinite(delay) || delay < 1 || delay > 2147483647 ? 1 : Math.trunc(delay)
      const id = ++timerCounter
      timers.set(id, {callback, args, interval})
      const failure = hostSend(JSON.stringify({type: 'timer', action: 'schedule', id, delay, interval}))
      if (failure) {
        timers.delete(id)
        throw new Error(failure)
      }
      return id
    }
    const clearTimer = value => {
      const id = typeof value === 'string' ? Number(value) : value
      if (!timers.has(id)) return
      const failure = hostSend(JSON.stringify({type: 'timer', action: 'clear', id}))
      if (failure) throw new Error(failure)
      timers.delete(id)
    }
    const invoke = (name, args) => {
      const requestId = ++requestCounter
      const operation = {observed: false, error: undefined}
      const promise = new Promise((resolve, reject) => {
        pending.set(requestId, {resolve, reject, operation})
        let failure
        try {
          failure = hostSend(JSON.stringify({type: 'invoke', requestId, name, args}))
        } catch {
          failure = 'Could not serialize Computer Use arguments'
        }
        if (failure) {
          pending.delete(requestId)
          reject(new Error(failure))
        } else {
          operations.push(operation)
        }
      })
      promise.catch(() => {})
      // Track whether submitted code consumes this operation. A separate
      // successful await must not hide an earlier ignored native failure.
      return {
        then(onFulfilled, onRejected) {
          operation.observed = true
          return promise.then(onFulfilled, onRejected)
        },
        catch(onRejected) {
          operation.observed = true
          return promise.catch(onRejected)
        },
        finally(onFinally) {
          operation.observed = true
          return promise.finally(onFinally)
        },
      }
    }
    const emit = content => {
      const failure = hostSend(JSON.stringify({type: 'emit', content}))
      if (failure) {
        throw new Error(failure)
      }
    }
    Object.defineProperties(globalThis, {
      __cuInvoke: {value: invoke},
      __cuEmit: {value: emit},
      setTimeout: {value: (callback, delay = 0, ...args) => scheduleTimer(callback, delay, args, false)},
      clearTimeout: {value: clearTimer},
      setInterval: {value: (callback, delay = 0, ...args) => scheduleTimer(callback, delay, args, true)},
      clearInterval: {value: clearTimer},
    })
    return {
      execute(fn) {
        // Close dispatch at the submitted promise's first settlement, before
        // the host's timer-error race adds another promise checkpoint. Keep
        // these handlers guest-created; no host callbacks enter the realm.
        return fn(this.scope, this.register, this.mark).then(value => {
          hostSend(JSON.stringify({type: 'submitted_done'}))
          return value
        }, error => {
          hostSend(JSON.stringify({type: 'submitted_done'}))
          throw error
        })
      },
      fireTimer(id) {
        const entry = timers.get(id)
        if (!entry) return
        if (!entry.interval) timers.delete(id)
        const result = Reflect.apply(entry.callback, undefined, entry.args)
        // Async callbacks can reject while the submitted code awaits another
        // event. Attribute that error to this cell instead of crashing the host.
        Promise.resolve(result).catch(error => {
          let message
          try { message = String(error?.message ?? error).slice(0, 8000) }
          catch { message = 'JavaScript timer callback failed' }
          hostSend(JSON.stringify({type: 'timer_error', message}))
        })
      },
      clearTimers() {
        timers.clear()
      },
      begin() {
        operations = []
        reached.clear()
        previous.clear()
        candidate = []
      },
      scope(writableConstants) {
        const writable = new Set(writableConstants)
        const values = new Proxy(Object.create(null), {
          get(_target, name) {
            const entry = saved.get(name)
            if (!entry && Reflect.has(globalThis, name)) return Reflect.get(globalThis, name)
            if (!entry) throw new ReferenceError(String(name) + ' is not defined')
            return entry.get()
          },
          set(_target, name, value) {
            const entry = saved.get(name)
            if (!entry) {
              if (!Reflect.has(globalThis, name)) throw new ReferenceError(String(name) + ' is not defined')
              if (!Reflect.set(globalThis, name, value)) throw new TypeError('Assignment to readonly global.')
              return true
            }
            if (entry.kind === 'const' && writable.has(name)) {
              // A prior cell's const may be reassigned with the compiler's
              // warning. Keep the shared slot, including readers in closures.
              entry.get()
              entry.get = () => value
              entry.set = next => { value = next }
            } else {
              if (entry.kind === 'const') {
                entry.get() // Preserve TDZ before the immutable-binding error.
                throw new TypeError('Assignment to constant variable.')
              }
              entry.set(value)
            }
            return true
          },
        })
        return {
          values,
          typeOf(name) {
            if (!saved.has(name) && !Reflect.has(globalThis, name)) return 'undefined'
            return typeof values[name]
          },
        }
      },
      register(entries) {
        candidate = entries
        for (const [name, kind, get, set] of entries) {
          previous.set(name, saved.get(name))
          saved.set(name, {kind, get, set})
        }
      },
      mark(...names) {
        for (const name of names) {
          reached.add(name)
        }
      },
      commit(succeeded) {
        for (const [name, _kind, getter, _setter, newlyHoisted] of candidate) {
          let retain = succeeded || !newlyHoisted || reached.has(name)
          try {
            getter()
          } catch {
            retain = false // The declaration was not initialized before failure.
          }
          if (!retain) {
            const old = previous.get(name)
            if (old) saved.set(name, old)
            else saved.delete(name)
          }
        }
        candidate = []
        previous.clear()
        return JSON.stringify([...saved].map(([name, entry]) => ({name, kind: entry.kind})))
      },
      response(serialized) {
        const message = JSON.parse(serialized)
        const entry = pending.get(message.requestId)
        if (!entry) {
          return
        }
        pending.delete(message.requestId)
        if (typeof message.error === 'string') {
          entry.operation.error = message.error
          entry.reject(new Error(message.error))
        } else {
          if (message.result?.isError === true) {
            const text = Array.isArray(message.result.content)
              ? message.result.content.filter(item => item?.type === 'text' && typeof item.text === 'string').map(item => item.text).join('\n')
              : ''
            entry.operation.error = text || 'Computer Use action failed.'
          }
          entry.resolve(message.result)
        }
      },
      unobservedError() {
        const failed = operations.find(operation => !operation.observed && typeof operation.error === 'string')
        return failed?.error
      },
    }
  })()