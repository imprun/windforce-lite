package source

import "testing"

func TestAuthURLUsesGitLabTokenForHTTP(t *testing.T) {
	got := authURL("http://gitlab.scraping.co.kr/gitlab/group/project.git", "secret-token")
	want := "http://oauth2:secret-token@gitlab.scraping.co.kr/gitlab/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLKeepsNonHTTPRepos(t *testing.T) {
	got := authURL("ssh://git@gitlab.scraping.co.kr/group/project.git", "secret-token")
	want := "ssh://git@gitlab.scraping.co.kr/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestRedactRemovesCredentials(t *testing.T) {
	got := redact("clone http://oauth2:secret-token@gitlab.scraping.co.kr/gitlab/group/project.git")
	want := "clone http://[REDACTED]@gitlab.scraping.co.kr/gitlab/group/project.git"
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}
