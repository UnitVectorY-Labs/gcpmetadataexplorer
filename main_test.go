package main

import (
	"strings"
	"testing"
)

func TestDecodeMetadataJSON(t *testing.T) {
	var data any
	input := `{"id":9007199254740993,"values":[12345678901234567890,1.2300e+10],"nested":{"enabled":true}}`
	if err := decodeMetadataJSON([]byte(input), &data); err != nil {
		t.Fatal(err)
	}
	metadata := data.(map[string]any)
	if got := getValueString(metadata["id"]); got != "9007199254740993" {
		t.Errorf("id = %q", got)
	}
	if got := getValueString(metadata["values"]); got != `[12345678901234567890,1.2300e+10]` {
		t.Errorf("values = %q", got)
	}
	if got := flattenMetadata(data); len(got) == 0 {
		t.Error("flattened metadata is empty")
	}
}

func TestDecodeMetadataJSONRejectsInvalidInput(t *testing.T) {
	for name, input := range map[string]string{
		"duplicate names":        `{"a":1,"a":2}`,
		"nested duplicate names": `{"a":{"b":1,"b":2}}`,
		"invalid UTF-8":          "{\"a\":\"\xff\"}",
		"trailing value":         `{} {}`,
		"malformed":              `{"a":`,
	} {
		t.Run(name, func(t *testing.T) {
			var data any
			if err := decodeMetadataJSON([]byte(input), &data); err == nil {
				t.Errorf("expected error for %s", strings.ToValidUTF8(input, "?"))
			}
		})
	}
}
