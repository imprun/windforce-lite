package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	inputPath := os.Getenv("WINDFORCE_INPUT_JSON")
	outputPath := os.Getenv("WINDFORCE_OUTPUT_JSON")
	if inputPath == "" || outputPath == "" {
		fmt.Fprintln(os.Stderr, "WINDFORCE_INPUT_JSON and WINDFORCE_OUTPUT_JSON are required")
		os.Exit(2)
	}

	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var input any
	if err := json.Unmarshal(inputData, &input); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	output := map[string]any{
		"ok":     true,
		"app":    os.Getenv("WINDFORCE_APP"),
		"action": os.Getenv("WINDFORCE_ACTION"),
		"input":  input,
	}
	outputData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	outputData = append(outputData, '\n')
	if err := os.WriteFile(outputPath, outputData, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
