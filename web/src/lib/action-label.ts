export function actionDisplayName(displayName?: string): string | null {
  const title = displayName?.trim();
  if (!title) return null;

  const [base] = title.split(/\s*(?:\(|—)\s*/u, 1);
  const concise = base.replace(/\s+(?:입력값|input)$/iu, "").trim();
  return concise || title;
}
