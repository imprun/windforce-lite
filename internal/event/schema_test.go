package event

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicControlPlaneEventSchemaMetadata(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "webhooks", "v1", "control-plane-event.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatalf("schema metadata = %#v", schema)
	}
	definitions, ok := schema["$defs"].(map[string]any)
	if !ok || definitions["releasePublishedData"] == nil || definitions["webhookTestData"] == nil {
		t.Fatalf("schema definitions = %#v", definitions)
	}
}
