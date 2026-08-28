package errors

import (
	stderrors "errors"
	"testing"
)

// TestWrapfDoesNotMutateArgs 验证 Wrapf 不污染调用方 args 切片的底层数组。
func TestWrapfDoesNotMutateArgs(t *testing.T) {
	inner := stderrors.New("sentinel")
	args := []any{"a", "b"}

	err1 := Wrapf(inner, "op %s %s", args...)
	err2 := Wrapf(inner, "op2 %s %s", args...)

	// 若 Wrapf 用 append(args, err) 且 args 容量足够，第二次调用会读到
	// 第一次 append 写入的 err，导致 err2 消息错乱。
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

// TestWrapfNilErr 验证 Wrapf(nil, ...) 返回 nil。
func TestWrapfNilErr(t *testing.T) {
	if err := Wrapf(nil, "op %s", "x"); err != nil {
		t.Fatalf("Wrapf(nil) 应返回 nil, got %v", err)
	}
}
