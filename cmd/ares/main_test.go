package main

import (
	"os"
	"strings"
	"testing"
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
