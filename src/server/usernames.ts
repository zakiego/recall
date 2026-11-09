// Username rules, shared by the server, the CLI and the forms.
export const USERNAME_MIN = 3
export const USERNAME_MAX = 32
export const USERNAME_PATTERN = /^[a-zA-Z0-9_.]+$/

export function normalizeUsername(u: string) {
  return u.trim().toLowerCase()
}

export function usernameProblem(u: string): string | null {
  const t = u.trim()
  if (t.length < USERNAME_MIN) return `Use at least ${USERNAME_MIN} characters.`
  if (t.length > USERNAME_MAX) return `Use at most ${USERNAME_MAX} characters.`
  if (!USERNAME_PATTERN.test(t)) return "Use letters, numbers, dots and underscores only."
  return null
}

// Better Auth requires an email on every user. Recall sends no email, so each
// user gets one under .invalid, a TLD reserved to never resolve.
export function placeholderEmail(username: string) {
  return `${normalizeUsername(username)}@users.recall.invalid`
}
