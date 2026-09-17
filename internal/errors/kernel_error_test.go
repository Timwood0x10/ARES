package errors

import (
	stderrors "errors"
	"strings"
	"testing"
)

func TestKernelErrorIsChain(t *testing.T) {
	inner := stderrors.New("sentinel")
	ke := Kernel("schedule", "no_capable_candidate", "task-1", "agent-2", inner)

	if !stderrors.Is(ke, inner) {
		t.Fatal("errors.Is 应穿透 KernelError 匹配到底层哨兵")
	}
	var kerr *KernelError
	if !stderrors.As(ke, &kerr) {
		t.Fatal("errors.As 应能匹配到 KernelError")
	}
	msg := ke.Error()
	if !strings.Contains(msg, "kernel:schedule") {
		t.Fatalf("Error() 缺少操作名: %s", msg)
	}
	if !strings.Contains(msg, "task=task-1") {
		t.Fatalf("Error() 缺少任务定位: %s", msg)
	}
	if !strings.Contains(msg, "agent=agent-2") {
		t.Fatalf("Error() 缺少代理定位: %s", msg)
	}
	if !strings.Contains(msg, "no_capable_candidate") {
		t.Fatalf("Error() 缺少错误码: %s", msg)
	}
	if !strings.Contains(msg, "sentinel") {
		t.Fatalf("Error() 缺少底层错误: %s", msg)
	}
}

func TestKernelErrorNilErr(t *testing.T) {
	ke := Kernel("ipc_reply", "orphan_channel", "", "agent-1", nil)
	if stderrors.Is(ke, stderrors.New("x")) {
		t.Fatal("nil 底层错误不应匹配")
	}
	if ke.Error() != "kernel:ipc_reply agent=agent-1 orphan_channel" {
		t.Fatalf("nil 底层错误格式化异常: %s", ke.Error())
	}
}
