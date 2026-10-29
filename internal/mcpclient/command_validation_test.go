package mcpclient

import (
	"strings"
	"testing"
)

// TestValidateStdioCommand pins the rule both stdio paths now share. A relative command is resolved
// against the process working directory, so "./helper" means a different binary depending on where
// the process was started and "../bin/x" reaches outside the deployment.
func TestValidateStdioCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wantErr string
	}{
		{"empty is refused", "", "required"},
		{"relative is refused", "./evil", "absolute"},
		{"parent traversal is refused", "../bin/x", "absolute"},
		{"bare name is refused", "npx", "absolute"},
		{"absolute is accepted", "/usr/local/bin/npx", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStdioCommand(tc.command)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
