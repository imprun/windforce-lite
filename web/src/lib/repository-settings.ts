import type { GitSource, ProbeResult } from "./api";
import { defaultGitCredentialPath } from "./git-credential";

export function repositoryLocationLocked(source: GitSource): boolean {
  return Boolean(source.last_synced_commit);
}

export function repositoryAccessLabel(source: GitSource): string {
  return source.creds_ref ? "Credential configured" : "Public repository";
}

export function reconnectCredentialPath(source: GitSource): string {
  return source.creds_ref || defaultGitCredentialPath(source.name);
}

export function probePassed(probe: ProbeResult | null): boolean {
  return Boolean(probe?.reachable && probe.branch_exists);
}
