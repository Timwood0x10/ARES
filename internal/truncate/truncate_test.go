package truncate

import "testing"

func TestWithEllipsis(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"", 10, ""},
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"你好世界", 2, "你好"},
		{"你好世界你好世界", 5, "你好..."},
		{"hello world", 8, "hello..."},
	}
	for _, tt := range tests {
		got := WithEllipsis(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("WithEllipsis(%q, %d) = %q; want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

func TestPlain(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"", 10, ""},
		{"hello", 10, "hello"},
		{"hello world", 5, "hello"},
		{"hello", 0, ""},
		{"hello", -1, ""},
		{"你好世界", 2, "你好"},
	}
	for _, tt := range tests {
		got := Plain(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("Plain(%q, %d) = %q; want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
