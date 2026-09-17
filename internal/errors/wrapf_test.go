package errors

import (
	stderrors "errors"
	"testing"
)

// TestWrapfDoesNotMutateArgs verifies Wrapf never mutates the caller's args backing array.
func TestWrapfDoesNotMutateArgs(t *testing.T) {
	inner := stderrors.New("sentinel")
	args := []any{"a", "b"}

	err1 := Wrapf(inner, "op %s %s", args...)
	err2 := Wrapf(inner, "op2 %s %s", args...)

	// If Wrapf used append(args, err) and args had spare capacity, a second
	// call would read the err written by the first append, corrupting err2's message.
	if err1.Error() != "op a b: sentinel" {
		t.Fatalf("err1 消息异常: %s", err1.Error())
	}
	if err2.Error() != "op2 a b: sentinel" {
		t.Fatalf("err2 消息异常（args 被污染）: %s", err2.Error())
	}
	if !stderrors.Is(err1, inner) {
		t.Fatal("Wrapf 应保留错误链")
	}
}

// TestWrapfNilErr verifies Wrapf(nil, ...) returns nil.
func TestWrapfNilErr(t *testing.T) {
	if err := Wrapf(nil, "op %s", "x"); err != nil {
		t.Fatalf("Wrapf(nil) 应返回 nil, got %v", err)
	}
}
