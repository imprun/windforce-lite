package runtime

import "testing"

func TestCuratedHostEnvDropsWorkerSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	t.Setenv("SECRET_KEY", "deadbeef")
	t.Setenv("S3_SECRET_KEY", "nope")
	t.Setenv("PATH", "/usr/bin")

	got := curatedHostEnv()
	seen := map[string]bool{}
	for _, value := range got {
		key := value
		for index, char := range value {
			if char == '=' {
				key = value[:index]
				break
			}
		}
		seen[key] = true
	}
	for _, leaked := range []string{"DATABASE_URL", "SECRET_KEY", "S3_SECRET_KEY"} {
		if seen[leaked] {
			t.Fatalf("%s leaked into curatedHostEnv: %#v", leaked, got)
		}
	}
	if !seen["PATH"] {
		t.Fatalf("PATH was not preserved: %#v", got)
	}
}
