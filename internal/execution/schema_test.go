package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-core/internal/contract"
)

type schemaBundleStore struct{}

func (schemaBundleStore) FetchTo(_ context.Context, destinationDir string, _ string, _ string, _ string) error {
	return os.WriteFile(filepath.Join(destinationDir, "defs.json"), []byte(`{
		"$defs":{"message":{"type":"string","minLength":1}}
	}`), 0o600)
}

func TestSchemaReaderValidatesRelativeReferencesFromPinnedBundle(t *testing.T) {
	reader := NewSchemaReader(context.Background(), schemaBundleStore{}, contract.Deployment{
		Workspace: "default", GitSourceID: "source-a", Commit: "commit-a",
	})
	defer reader.Close()
	schema := json.RawMessage(`{
		"type":"object",
		"required":["message"],
		"properties":{"message":{"$ref":"defs.json#/$defs/message"}}
	}`)
	if err := reader.Validate("input.schema.json", schema, json.RawMessage(`{"message":"hello"}`)); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	err := reader.Validate("input.schema.json", schema, json.RawMessage(`{"message":""}`))
	var validation *InputValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("invalid input error = %T %v", err, err)
	}
}

func TestSchemaReaderRejectsEscapingMaterializedSchemaPath(t *testing.T) {
	reader := NewSchemaReader(context.Background(), schemaBundleStore{}, contract.Deployment{})
	if _, err := reader.Read("../input.schema.json", json.RawMessage(`{"type":"object"}`)); err == nil {
		t.Fatal("escaping schema path was accepted")
	}
}
