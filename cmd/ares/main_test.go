package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// TestParseRunArgsStripsOnlyLeadingSubcommand locks E-14: only the FIRST
// "run" token is the subcommand; a later "run" is legitimate prompt text.
// The old behavior dropped every "run" word, silently rewriting the user's
// prompt (`ares run please run the tests` became "please the tests").
func TestParseRunArgsStripsOnlyLeadingSubcommand(t *testing.T) {
	orig := os.Args
	defer func() { os.Args = orig }()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			"second_run_is_prompt_text",
			[]string{"ares", "run", "please", "run", "the", "tests"},
			"please run the tests",
		},
		{
			"config_flag_pair_removed",
			[]string{"ares", "run", "-c", "x.yaml", "prompt here"},
			"prompt here",
		},
		{
			"config_equals_form_removed",
			[]string{"ares", "run", "--config=x.yaml", "hello"},
			"hello",
		},
		{
			"no_subcommand_still_strips_config",
			[]string{"ares", "--config=x.yaml", "hi"},
			"hi",
		},
		{
			"subcommand_only_yields_empty_prompt",
			[]string{"ares", "run"},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			os.Args = tc.args
			if got := strings.Join(parseRunArgs(), " "); got != tc.want {
				t.Fatalf("parseRunArgs() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunInitGeneratesProtectedAPIKey locks the init template credential
// contract: the generated ares.yaml embeds a non-empty security.api_key,
// parses as YAML carrying that key, and is written owner-only (0600) — the
// file holds a live control-plane credential.
func TestRunInitGeneratesProtectedAPIKey(t *testing.T) {
	dir := t.TempDir()
	cmd := &cobra.Command{Use: "init"}
	cmd.Flags().String("dir", dir, "")
	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ares.yaml"))
	if err != nil {
		t.Fatalf("read generated ares.yaml: %v", err)
	}
	var parsed struct {
		Security struct {
			APIKey string `yaml:"api_key"`
		} `yaml:"security"`
		Server struct {
			DefaultCapability string `yaml:"default_capability"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("generated ares.yaml must parse: %v", err)
	}
	if len(parsed.Security.APIKey) < 32 {
		t.Fatalf("generated security.api_key = %q, want a >=32-char random key", parsed.Security.APIKey)
	}
	if parsed.Server.DefaultCapability != "ares/plan" {
		t.Fatalf("default_capability = %q, want ares/plan", parsed.Server.DefaultCapability)
	}
	info, err := os.Stat(filepath.Join(dir, "ares.yaml"))
	if err != nil {
		t.Fatalf("stat ares.yaml: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("ares.yaml mode = %o, want 0600 (file carries the control-plane credential)", perm)
	}
}
