import { describe, expect, test } from "bun:test";
import { defaultGitCredentialPath, gitCredentialSecretValue } from "./git-credential";

describe("defaultGitCredentialPath", () => {
  test("keeps readable source names", () => {
    expect(defaultGitCredentialPath("정부24")).toBe("git/정부24/credential");
    expect(defaultGitCredentialPath("Coupang Eats")).toBe("git/Coupang-Eats/credential");
  });

  test("normalizes path separators and empty names", () => {
    expect(defaultGitCredentialPath("team/source")).toBe("git/team-source/credential");
    expect(defaultGitCredentialPath("  ")).toBe("git/source/credential");
  });
});

describe("gitCredentialSecretValue", () => {
  test("serializes PAT credentials", () => {
    expect(gitCredentialSecretValue("pat", " token-value ", "", "")).toBe(
      JSON.stringify({ type: "pat", token: "token-value" }),
    );
  });

  test("serializes basic credentials", () => {
    expect(gitCredentialSecretValue("basic", "", " junsik ", "pw")).toBe(
      JSON.stringify({ type: "basic", username: "junsik", password: "pw" }),
    );
  });
});
