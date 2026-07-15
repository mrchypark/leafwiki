import '@testing-library/jest-dom'

// Some Node/Vitest workers shadow jsdom storage with an undefined global.
if (typeof window !== 'undefined' && !window.localStorage) {
  const values = new Map<string, string>()
  Object.defineProperty(window, 'localStorage', {
    value: {
      get length() {
        return values.size
      },
      clear: () => values.clear(),
      getItem: (key: string) => values.get(key) ?? null,
      key: (index: number) => [...values.keys()][index] ?? null,
      removeItem: (key: string) => values.delete(key),
      setItem: (key: string, value: string) => values.set(key, value),
    } satisfies Storage,
    configurable: true,
  })
}
