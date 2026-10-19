export const REVIEW_HOSTS = [
  { id: 'github', label: 'GitHub', example: 'owner/repo', word: 'pull request' },
  { id: 'gitlab', label: 'GitLab', example: 'group/project', word: 'merge request' },
];

export function copy(text) {
  try {
    navigator.clipboard?.writeText(text);
  } catch {
    /* clipboard may be blocked; the value is visible anyway */
  }
}
