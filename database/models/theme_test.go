package models

import "testing"

func TestThemeValidateConfiguration(t *testing.T) {
	tests := map[string]struct {
		theme   Theme
		wantErr bool
	}{
		"managed empty data": {
			theme: Theme{
				Configuration: Configuration{Type: ThemeConfigurationManaged},
			},
		},
		"missing type defaults to managed": {
			theme: Theme{},
		},
		"raw html": {
			theme: Theme{
				Configuration: Configuration{
					Type: ThemeConfigurationRaw,
					Data: "<!doctype html><title>raw</title>",
				},
			},
		},
		"raw rejects empty html": {
			theme: Theme{
				Configuration: Configuration{
					Type: ThemeConfigurationRaw,
					Data: "  ",
				},
			},
			wantErr: true,
		},
		"redirect relative path": {
			theme: Theme{
				Configuration: Configuration{
					Type: ThemeConfigurationRedirect,
					Data: "/dashboard?tab=nodes",
				},
			},
		},
		"redirect rejects absolute url": {
			theme: Theme{
				Configuration: Configuration{
					Type: ThemeConfigurationRedirect,
					Data: "https://example.com",
				},
			},
			wantErr: true,
		},
		"redirect rejects parent traversal": {
			theme: Theme{
				Configuration: Configuration{
					Type: ThemeConfigurationRedirect,
					Data: "/../admin",
				},
			},
			wantErr: true,
		},
		"unknown type": {
			theme: Theme{
				Configuration: Configuration{Type: "hosted"},
			},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.theme.ValidateConfiguration()
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeThemeRedirectTarget(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
		ok    bool
	}{
		"leading slash": {
			input: "/nodes",
			want:  "/nodes",
			ok:    true,
		},
		"missing leading slash": {
			input: "nodes",
			want:  "/nodes",
			ok:    true,
		},
		"parent prefix points to root path": {
			input: "../settings",
			want:  "/settings",
			ok:    true,
		},
		"multiple parent prefixes point to root path": {
			input: "../../settings/site",
			want:  "/settings/site",
			ok:    true,
		},
		"query and fragment": {
			input: "nodes?id=1#cpu",
			want:  "/nodes?id=1#cpu",
			ok:    true,
		},
		"absolute parent traversal": {
			input: "/../admin",
		},
		"nested parent traversal": {
			input: "nodes/../admin",
		},
		"protocol relative": {
			input: "//example.com",
		},
		"backslash": {
			input: `\admin`,
		},
		"absolute url": {
			input: "https://example.com",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := NormalizeThemeRedirectTarget(tt.input)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("target = %q, want %q", got, tt.want)
			}
		})
	}
}
