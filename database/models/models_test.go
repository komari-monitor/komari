package models

import "testing"

func TestStringArrayScanAcceptsSQLiteTextShapes(t *testing.T) {
	tests := map[string]interface{}{
		"bytes": []byte(`["a","b"]`),
		"text":  `["a","b"]`,
		"empty": "",
		"nil":   nil,
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			var got StringArray
			if err := got.Scan(input); err != nil {
				t.Fatalf("scan StringArray: %v", err)
			}
			if input == "" || input == nil {
				if len(got) != 0 {
					t.Fatalf("expected empty StringArray, got %v", got)
				}
				return
			}
			if len(got) != 2 || got[0] != "a" || got[1] != "b" {
				t.Fatalf("unexpected StringArray: %v", got)
			}
		})
	}
}
