package runtime

import (
	"encoding/json"
	"testing"
)

func TestSourceReadyRecordChangesAcrossRuntimeInputs(t *testing.T) {
	for _, language := range []string{"python", "typescript", "go"} {
		baseline := sourceReadyRecord{
			Version:  sourcePrepareVersion,
			Language: language,
			Runtime:  "runtime-a",
			Platform: "platform-a",
			SDK:      "sdk-a",
		}
		baselineJSON, err := json.Marshal(baseline)
		if err != nil {
			t.Fatal(err)
		}
		for name, changed := range map[string]sourceReadyRecord{
			"runtime":  {Version: sourcePrepareVersion, Language: language, Runtime: "runtime-b", Platform: "platform-a", SDK: "sdk-a"},
			"platform": {Version: sourcePrepareVersion, Language: language, Runtime: "runtime-a", Platform: "platform-b", SDK: "sdk-a"},
			"sdk":      {Version: sourcePrepareVersion, Language: language, Runtime: "runtime-a", Platform: "platform-a", SDK: "sdk-b"},
		} {
			changedJSON, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if string(changedJSON) == string(baselineJSON) {
				t.Fatalf("%s %s change did not invalidate ready record", language, name)
			}
		}
	}
}

func TestSourceSDKDigestsAreStableAndLanguageSpecific(t *testing.T) {
	seen := map[string]string{}
	for _, language := range []string{"python", "typescript", "go"} {
		first, err := sourceSDKDigest(language)
		if err != nil {
			t.Fatal(err)
		}
		second, err := sourceSDKDigest(language)
		if err != nil {
			t.Fatal(err)
		}
		if first == "" || first != second {
			t.Fatalf("%s SDK digest is not stable: %q != %q", language, first, second)
		}
		if previous, exists := seen[first]; exists {
			t.Fatalf("%s and %s SDK digests unexpectedly match", previous, language)
		}
		seen[first] = language
	}
}

func TestRuntimeIdentityCommandSupportsEveryLanguage(t *testing.T) {
	tests := []struct {
		language   string
		executable string
	}{
		{language: "python", executable: "python-bin"},
		{language: "typescript", executable: "bun-bin"},
		{language: "go", executable: "go-bin"},
	}
	for _, test := range tests {
		executable, args := runtimeIdentityCommand(test.language, "python-bin", "bun-bin", "go-bin")
		if executable != test.executable || len(args) == 0 {
			t.Fatalf("runtimeIdentityCommand(%q) = %q %#v", test.language, executable, args)
		}
	}
}
