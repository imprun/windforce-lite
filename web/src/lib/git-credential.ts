export type GitAuthMethod = "none" | "pat" | "basic";

export function defaultGitCredentialPath(sourceName: string): string {
  const segment = sourceName
    .trim()
    .replace(/[\/\\\s\x00-\x1f\x7f]+/g, "-")
    .replace(/-+/g, "-")
    .replace(/^-|-$/g, "");
  const safeSegment = segment && segment !== "." && segment !== ".." ? segment : "source";
  return `git/${safeSegment}/credential`;
}

export function gitCredentialSecretValue(
  authMethod: GitAuthMethod,
  accessToken: string,
  username: string,
  password: string,
): string {
  if (authMethod === "pat") {
    const token = accessToken.trim();
    if (!token) return "";
    return JSON.stringify({ type: "pat", token });
  }
  if (authMethod === "basic") {
    const login = username.trim();
    if (!login || !password) return "";
    return JSON.stringify({ type: "basic", username: login, password });
  }
  return "";
}
