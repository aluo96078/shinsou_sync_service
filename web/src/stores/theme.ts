import { ref } from 'vue'

export type ThemeChoice = 'system' | 'light' | 'dark'
export const THEMES: readonly ThemeChoice[] = ['system', 'light', 'dark']
const STORAGE_KEY = 'shinsou.theme'

function load(): ThemeChoice {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'light' || v === 'dark' ? v : 'system'
  } catch {
    return 'system'
  }
}

export const theme = ref<ThemeChoice>(load())

export function applyTheme(): void {
  const root = document.documentElement
  if (theme.value === 'system') delete root.dataset.theme
  else root.dataset.theme = theme.value
}

export function setTheme(next: ThemeChoice): void {
  theme.value = next
  applyTheme()
  try {
    if (next === 'system') localStorage.removeItem(STORAGE_KEY)
    else localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // Not remembered; still applied for this visit.
  }
}
