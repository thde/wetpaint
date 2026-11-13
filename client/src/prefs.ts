/** localStorage that shrugs when storage is unavailable (private browsing). */
export const prefs = {
  get(key: string): string | null {
    try {
      return localStorage.getItem(key);
    } catch {
      return null;
    }
  },
  set(key: string, value: string): void {
    try {
      localStorage.setItem(key, value);
    } catch {
      // Nothing to do: the preference simply will not persist.
    }
  },
};
