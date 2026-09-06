const appBase = import.meta.env.BASE_URL.replace(/\/$/, '')

/** Build an application URL that works in Vite dev and behind the fnOS gateway. */
export const appPath = (path: string): string => {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${appBase}${normalizedPath}`
}
