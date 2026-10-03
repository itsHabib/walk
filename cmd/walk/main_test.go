package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A model is written and read by people, so rewriting its pin must leave every
// other byte alone. An earlier version round-tripped the document through a Go
// map, which sorted the keys and put every expression element on its own line.
func TestRepinLeavesTheRestOfTheFileAlone(t *testing.T) {
	const doc = `{
  "name": "example",
  "pin": 7,

  "vars": {
    "s":  ["a", "b"]
  },
  "actions": [
    {"name": "go", "guard": ["==", "s", "a"], "effect": {"s": "b"}}
  ]
}
`
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repin(path, 42); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(doc, `"pin": 7`, `"pin": 42`, 1)
	if string(got) != want {
		t.Fatalf("repin changed more than the pin:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRepinAddsAMissingPin(t *testing.T) {
	const doc = `{
  "name": "example",
  "vars": {"s": ["a", "b"]},
  "actions": [{"name": "go", "guard": ["==", "s", "a"], "effect": {"s": "b"}}]
}
`
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repin(path, 3); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, raw)
	}
	if m["pin"].(float64) != 3 {
		t.Fatalf("pin is %v, want 3", m["pin"])
	}
	if m["name"] != "example" {
		t.Fatalf("name was lost: %v", m["name"])
	}
	if !strings.Contains(string(raw), `"vars": {"s": ["a", "b"]}`) {
		t.Fatalf("original formatting was not preserved:\n%s", raw)
	}
}

// A pin that lands inside a string, or any other edit that breaks the
// document, must fail before the file is written rather than after.
func TestRepinRefusesToWriteInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := repin(path, 1); err == nil {
		t.Fatal("repin accepted a file that is not JSON")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "not json at all" {
		t.Fatalf("the file was modified despite the error: %q", got)
	}
}
