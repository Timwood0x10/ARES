package output

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── parseArrayFormat: fix path and non-array prefix ─────────────────────────

func TestParseArrayFormat_FixBrokenJSON(t *testing.T) {
	p := NewParser()
	// Broken JSON array: trailing comma + unquoted key inside object
	input := `[{item_id:"1",name:"a",},]`
	result, err := p.parseArrayFormat(input)
	if err != nil {
		t.Fatalf("parseArrayFormat: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(result.Items))
	}
	if result.Items[0].ItemID != "1" {
		t.Errorf("ItemID = %q", result.Items[0].ItemID)
	}
}

func TestParseArrayFormat_NonArrayPrefix(t *testing.T) {
	p := NewParser()
	// Not starting with '[' — will be wrapped in brackets
	input := `{"item_id":"x","name":"n","category":"casual","price":5}`
	result, err := p.parseArrayFormat(input)
	if err != nil {
		t.Fatalf("parseArrayFormat: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items len = %d, want 1", len(result.Items))
	}
}

func TestParseArrayFormat_UnfixableJSON(t *testing.T) {
	p := NewParser()
	_, err := p.parseArrayFormat(`[not json at all {{{`)
	if err == nil {
		t.Error("expected error for unfixable JSON")
	}
}

func TestParseArrayFormat_NoFixJSON(t *testing.T) {
	p := &Parser{fixJSON: false, inputValidator: NewInputValidator()}
	_, err := p.parseArrayFormat(`[{broken}`)
	if err == nil {
		t.Error("expected error when fixJSON is false")
	}
}

// ── ParseRecommendResult: fix path and fallback ─────────────────────────────

func TestParseRecommendResult_FixObjectPath(t *testing.T) {
	p := NewParser()
	// Object with unquoted keys — fixJSONString should repair it
	input := `{items:[{item_id:"1",name:"a"}]}`
	result, err := p.ParseRecommendResult(input)
	if err != nil {
		t.Fatalf("ParseRecommendResult: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("items len = %d", len(result.Items))
	}
}

func TestParseRecommendResult_FallbackToArray(t *testing.T) {
	p := NewParser()
	// Neither valid object nor fixable — falls through to parseArrayFormat
	input := `{items:[{item_id:"1"}]}`
	result, err := p.ParseRecommendResult(input)
	if err != nil {
		t.Fatalf("ParseRecommendResult: %v", err)
	}
	_ = result
}

func TestParseRecommendResult_NoFixJSON(t *testing.T) {
	p := &Parser{fixJSON: false, inputValidator: NewInputValidator()}
	// Invalid object JSON — fixJSON is false so it falls to parseArrayFormat
	_, err := p.ParseRecommendResult(`{invalid json here}`)
	if err == nil {
		t.Error("expected error when fixJSON is false and object is invalid")
	}
}

// ── fixJSONString: failed-to-fix path ───────────────────────────────────────

func TestFixJSONString_Unfixable(t *testing.T) {
	p := NewParser()
	_, err := p.fixJSONString(`{"key": "value"`) // missing closing brace
	if err == nil {
		t.Error("expected error for unfixable JSON")
	}
}

func TestFixJSONString_AlreadyValid(t *testing.T) {
	p := NewParser()
	fixed, err := p.fixJSONString(`{"key":"value"}`)
	if err != nil {
		t.Fatalf("fixJSONString: %v", err)
	}
	if fixed != `{"key":"value"}` {
		t.Errorf("fixed = %q", fixed)
	}
}

// ── scanFixJSON: block comments and unquoted keys after { ──────────────────

func TestScanFixJSON_BlockComment(t *testing.T) {
	input := `{"key": /* comment */ "value"}`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["key"] != "value" {
		t.Errorf("result = %v", result)
	}
}

func TestScanFixJSON_UnclosedBlockComment(t *testing.T) {
	input := `{"key": "value" /* unclosed`
	fixed := scanFixJSON(input)
	// The unclosed comment is stripped to end — the result may not be valid
	// JSON, but scanFixJSON must not panic and must return a string.
	_ = fixed
}

func TestScanFixJSON_UnquotedKeyAfterBrace(t *testing.T) {
	input := `{name:"test",value:42}`
	fixed := scanFixJSON(input)
	var result map[string]any
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["name"] != "test" {
		t.Errorf("name = %v", result["name"])
	}
}

func TestScanFixJSON_LineComment(t *testing.T) {
	input := `{"key": "value"} // trailing comment`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["key"] != "value" {
		t.Errorf("result = %v", result)
	}
}

func TestScanFixJSON_TrailingCommaInObject(t *testing.T) {
	input := `{"a":1, "b":2,}`
	fixed := scanFixJSON(input)
	var result map[string]int
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["a"] != 1 || result["b"] != 2 {
		t.Errorf("result = %v", result)
	}
}

func TestScanFixJSON_TrailingCommaInArray(t *testing.T) {
	input := `[1, 2, 3,]`
	fixed := scanFixJSON(input)
	var result []int
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if len(result) != 3 {
		t.Errorf("result = %v", result)
	}
}

func TestScanFixJSON_SingleQuoteValue(t *testing.T) {
	input := `{'key': 'value with spaces'}`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["key"] != "value with spaces" {
		t.Errorf("result = %v", result)
	}
}

func TestScanFixJSON_EscapedCharsInSingleQuote(t *testing.T) {
	input := `{'path': 'C:\\Users\\test'}`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if !strings.Contains(result["path"], "Users") {
		t.Errorf("path = %q", result["path"])
	}
}

// ── ParseGeneric: fix path and no-fixJSON path ──────────────────────────────

func TestParseGeneric_FixPath(t *testing.T) {
	p := NewParser()
	var target map[string]any
	input := `{key:"value",num:42}`
	err := p.ParseGeneric(input, &target)
	if err != nil {
		t.Fatalf("ParseGeneric: %v", err)
	}
	if target["key"] != "value" {
		t.Errorf("key = %v", target["key"])
	}
}

func TestParseGeneric_FixFails(t *testing.T) {
	p := NewParser()
	var target map[string]any
	err := p.ParseGeneric(`{completely broken [[[`, &target)
	if err == nil {
		t.Error("expected error when fix fails")
	}
}

func TestParseGeneric_FixProducesInvalidJSON(t *testing.T) {
	p := NewParser()
	var target map[string]any
	// scanFixJSON runs but result still invalid
	err := p.ParseGeneric(`{key:value without colon or quote`, &target)
	if err == nil {
		t.Error("expected error when fix produces invalid JSON")
	}
}

// ── ParseJSON: fix path and no-fixJSON path ─────────────────────────────────

func TestParseJSON_FixPath(t *testing.T) {
	p := NewParser()
	result, err := p.ParseJSON(`{key:"value"}`)
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	if result["key"] != "value" {
		t.Errorf("key = %v", result["key"])
	}
}

func TestParseJSON_FixFails(t *testing.T) {
	p := NewParser()
	_, err := p.ParseJSON(`{broken [[[`)
	if err == nil {
		t.Error("expected error when fix fails")
	}
}

func TestParseJSON_NoFixJSON(t *testing.T) {
	p := &Parser{fixJSON: false, inputValidator: NewInputValidator()}
	_, err := p.ParseJSON(`{invalid}`)
	if err == nil {
		t.Error("expected error when fixJSON is false")
	}
}

func TestParseJSON_FixProducesInvalid(t *testing.T) {
	p := NewParser()
	_, err := p.ParseJSON(`{key:value no colon`)
	if err == nil {
		t.Error("expected error when fix produces invalid JSON")
	}
}

// ── ParseArray: array-too-large path ────────────────────────────────────────

func TestParseArray_ArrayTooLarge(t *testing.T) {
	p := NewParser()
	// Build an array exceeding MaxArrayLength (1000)
	items := make([]string, MaxArrayLength+1)
	for i := range items {
		items[i] = `"x"`
	}
	input := `[` + strings.Join(items, ",") + `]`
	_, err := p.ParseArray(input)
	if err == nil {
		t.Error("expected ErrArrayTooLarge for oversized array")
	}
}

// ── ParseJSONSlice: fix path and array-too-large ────────────────────────────

func TestParseJSONSlice_FixPath(t *testing.T) {
	p := NewParser()
	result, err := p.ParseJSONSlice(`["a","b",]`)
	if err != nil {
		t.Fatalf("ParseJSONSlice: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("len = %d, want 2", len(result))
	}
}

func TestParseJSONSlice_FixFails(t *testing.T) {
	p := NewParser()
	_, err := p.ParseJSONSlice(`[broken [[[`)
	if err == nil {
		t.Error("expected error when fix fails")
	}
}

func TestParseJSONSlice_ArrayTooLarge(t *testing.T) {
	p := NewParser()
	items := make([]string, MaxArrayLength+1)
	for i := range items {
		items[i] = `"x"`
	}
	input := `[` + strings.Join(items, ",") + `]`
	_, err := p.ParseJSONSlice(input)
	if err == nil {
		t.Error("expected ErrArrayTooLarge")
	}
}

func TestParseJSONSlice_NoFixJSON(t *testing.T) {
	p := &Parser{fixJSON: false, inputValidator: NewInputValidator()}
	_, err := p.ParseJSONSlice(`[invalid json`)
	if err == nil {
		t.Error("expected error when fixJSON is false")
	}
}

func TestParseJSONSlice_FixProducesInvalid(t *testing.T) {
	p := NewParser()
	_, err := p.ParseJSONSlice(`[broken without close`)
	if err == nil {
		t.Error("expected error when fix produces invalid JSON")
	}
}

// ── findMatchingBracket: unmatched bracket path ─────────────────────────────

func TestFindMatchingBracket_Unmatched(t *testing.T) {
	tests := []struct {
		name  string
		input string
		start int
		open  byte
		close byte
	}{
		{"unclosed_object", `{"key": "value"`, 0, '{', '}'},
		{"unclosed_array", `[1, 2, 3`, 0, '[', ']'},
		{"no_close_after_open", `{`, 0, '{', '}'},
		{"string_never_closes", `{"key": "unclosed`, 0, '{', '}'},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findMatchingBracket(tt.input, tt.start, tt.open, tt.close)
			if got != -1 {
				t.Errorf("findMatchingBracket(%q) = %d, want -1", tt.input, got)
			}
		})
	}
}

func TestFindMatchingBracket_NestedStrings(t *testing.T) {
	input := `{"key": "value with } brace", "other": 1}`
	end := findMatchingBracket(input, 0, '{', '}')
	if end != len(input) {
		t.Errorf("end = %d, want %d", end, len(input))
	}
}

func TestFindMatchingBracket_EscapedQuotes(t *testing.T) {
	input := `{"key": "escaped \" quote"}`
	end := findMatchingBracket(input, 0, '{', '}')
	if end != len(input) {
		t.Errorf("end = %d, want %d", end, len(input))
	}
}

// ── extractJSON: markdown block that is not JSON ────────────────────────────

func TestExtractJSON_MarkdownNotJSON(t *testing.T) {
	p := NewParser()
	got := p.extractJSON("```json\njust plain text\n```")
	if got != "" {
		t.Errorf("extractJSON = %q, want empty for non-JSON markdown", got)
	}
}

func TestExtractJSON_ArrayBeforeObject(t *testing.T) {
	p := NewParser()
	input := `Here is data: [{"id":1},{"id":2}] and more`
	got := p.extractJSON(input)
	if !strings.HasPrefix(got, "[") {
		t.Errorf("extractJSON = %q, want starts with [", got)
	}
	if !strings.HasSuffix(got, "]") {
		t.Errorf("extractJSON = %q, want ends with ]", got)
	}
}

// ── ParseRecommendResult: input too long ────────────────────────────────────

func TestParseRecommendResult_InputTooLong(t *testing.T) {
	p := NewParser()
	long := strings.Repeat("x", MaxInputLength+1)
	_, err := p.ParseRecommendResult(long)
	if err == nil {
		t.Error("expected error for input exceeding MaxInputLength")
	}
}

// ── ParseJSON / ParseArray: input too long ──────────────────────────────────

func TestParseJSON_InputTooLong(t *testing.T) {
	p := NewParser()
	long := strings.Repeat("x", MaxInputLength+1)
	_, err := p.ParseJSON(long)
	if err == nil {
		t.Error("expected error for input exceeding MaxInputLength")
	}
}

func TestParseArray_InputTooLong(t *testing.T) {
	p := NewParser()
	long := strings.Repeat("x", MaxInputLength+1)
	_, err := p.ParseArray(long)
	if err == nil {
		t.Error("expected error for input exceeding MaxInputLength")
	}
}
