package sdk

// MustNew is the zero-parameter quickstart entry point. It loads the
// configuration from ./ares.yaml — the single configuration entry point (no
// environment variable is read anywhere in this package) — enables default
// memory (compression-only when no embedding service is configured), and
// returns a ready-to-use Runtime.
//
// MustNew panics when the config file is missing/invalid or when the Runtime
// cannot be constructed, mirroring regexp.MustCompile's fail-fast philosophy.
// Use New / WithConfig for production code that needs to handle errors
// gracefully.
//
// Quick start:
//
//	ares := sdk.MustNew() // reads ./ares.yaml
//	defer ares.Close()
//	agent := ares.NewAgent("assistant", sdk.WithInstruction("You are helpful."))
//	result, _ := agent.Run(ctx, "hello")
func MustNew() *Runtime {
	cfg, err := LoadConfigFile("./ares.yaml")
	if err != nil {
		panic("ares: load ./ares.yaml: " + err.Error() +
			" — create an ares.yaml with an llm section (the single config entry point)")
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		panic("ares: " + err.Error())
	}
	rt, err := New(opts...)
	if err != nil {
		panic("ares: " + err.Error())
	}
	return rt
}
