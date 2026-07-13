import { describe, expect, test } from "bun:test";
import type { GitSource } from "./api";
import {
  probePassed,
  reconnectCredentialPath,
  repositoryAccessLabel,
  repositoryLocationLocked,
} from "./repository-settings";

const source: GitSource = {
  id: 2,
  workspace_id: "default",
  name: "MLMWGM",
  repo_url: "https://example.test/gov24.git",
  branch: "main",
  subpath: "",
  creds_ref: "git/gov24/credential",
  kind: "external",
  created_at: "2026-07-13T00:00:00Z",
};

describe("repository settings policy", () => {
  test("locks repository location after the first release", () => {
    expect(repositoryLocationLocked(source)).toBe(false);
    expect(repositoryLocationLocked({ ...source, last_synced_commit: "abc123" })).toBe(true);
  });

  test("does not expose credential paths in access labels", () => {
    expect(repositoryAccessLabel(source)).toBe("Credential configured");
    expect(repositoryAccessLabel({ ...source, creds_ref: "" })).toBe("Public repository");
  });

  test("keeps an existing credential path when rotating a credential", () => {
    expect(reconnectCredentialPath(source)).toBe("git/gov24/credential");
    expect(reconnectCredentialPath({ ...source, creds_ref: "", name: "Gov 24" })).toBe("git/Gov-24/credential");
  });

  test("requires both reachability and the selected branch", () => {
    expect(probePassed({ reachable: true, branch_exists: true })).toBe(true);
    expect(probePassed({ reachable: true, branch_exists: false })).toBe(false);
    expect(probePassed({ reachable: false, branch_exists: true })).toBe(false);
    expect(probePassed(null)).toBe(false);
  });
});
