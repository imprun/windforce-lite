package runtime

import "testing"

func TestCuratedHostEnvDropsWorkerSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	t.Setenv("SECRET_KEY", "deadbeef")
	t.Setenv("S3_SECRET_KEY", "nope")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("LOG_LEVEL", "DEBUG")
	t.Setenv("PYTHON_LOG_LEVEL", "DEBUG")
	t.Setenv("PYTHONUNBUFFERED", "1")
	t.Setenv("PYTHONIOENCODING", "utf-8")

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
	for _, preserved := range []string{"LOG_LEVEL", "PYTHON_LOG_LEVEL", "PYTHONUNBUFFERED", "PYTHONIOENCODING"} {
		if !seen[preserved] {
			t.Fatalf("%s was not preserved: %#v", preserved, got)
		}
	}
}
