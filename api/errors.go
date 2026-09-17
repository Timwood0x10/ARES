package ares

import "github.com/Timwood0x10/ares/sdk"

// ErrHumanInputUnsupported is returned when WithHumanInput is used on the
// L2 execution path, which does not support human approval gates.
var ErrHumanInputUnsupported = sdk.ErrHumanInputUnsupported

// ErrDistillDepsMissing is returned when distillation dependencies are
// unavailable (e.g., no embedding service configured).
var ErrDistillDepsMissing = sdk.ErrDistillDepsMissing

// FriendlyErr wraps an LLM error with an actionable hint based on the
// provider and error type.
func FriendlyErr(scope string, provider LLMProvider, origErr error) error {
	return sdk.FriendlyErr(scope, provider, origErr)
}
