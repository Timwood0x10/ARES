package monitoring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPManager implements MCPManager for testing.
type mockMCPManager struct {
	tools  []MCPToolInfo
	result *MCPToolResult
	err    error
}

func (m *mockMCPManager) ListTools(_ context.Context) ([]MCPToolInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.tools, nil
}

func (m *mockMCPManager) CallTool(_ context.Context, _ string, _ map[string]any) (*MCPToolResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestMCPManager_ListTools(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockMCPManager
		wantLen int
		wantErr error
	}{
		{
			name: "success with tools",
			mock: &mockMCPManager{
				tools: []MCPToolInfo{
					{Name: "read_file", Description: "Read a file"},
					{Name: "write_file", Description: "Write a file"},
				},
			},
			wantLen: 2,
		},
		{
			name: "success empty",
			mock: &mockMCPManager{
				tools: []MCPToolInfo{},
			},
			wantLen: 0,
		},
		{
			name:    "error",
			mock:    &mockMCPManager{err: errors.New("connection refused")},
			wantErr: errors.New("connection refused"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools, err := tt.mock.ListTools(context.Background())
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr.Error())
				return
			}
			require.NoError(t, err)
			assert.Len(t, tools, tt.wantLen)
		})
	}
}

func TestMCPManager_CallTool(t *testing.T) {
	tests := []struct {
		name    string
		mock    *mockMCPManager
		tool    string
		args    map[string]any
		want    *MCPToolResult
		wantErr bool
	}{
		{
			name: "success",
			mock: &mockMCPManager{
				result: &MCPToolResult{
					ToolName: "read_file",
					Output:   map[string]any{"content": "hello"},
				},
			},
			tool: "read_file",
			args: map[string]any{"path": "/tmp/test"},
			want: &MCPToolResult{
				ToolName: "read_file",
				Output:   map[string]any{"content": "hello"},
			},
		},
		{
			name: "tool error",
			mock: &mockMCPManager{
				result: &MCPToolResult{
					ToolName: "read_file",
					IsError:  true,
					Error:    "file not found",
				},
			},
			tool: "read_file",
			args: map[string]any{"path": "/missing"},
			want: &MCPToolResult{
				ToolName: "read_file",
				IsError:  true,
				Error:    "file not found",
			},
		},
		{
			name:    "call error",
			mock:    &mockMCPManager{err: errors.New("timeout")},
			tool:    "slow_tool",
			args:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.mock.CallTool(context.Background(), tt.tool, tt.args)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want.ToolName, result.ToolName)
			assert.Equal(t, tt.want.IsError, result.IsError)
			assert.Equal(t, tt.want.Error, result.Error)
		})
	}
}

func TestMonitorPlugin_ListMCPTools(t *testing.T) {
	tests := []struct {
		name    string
		mcp     MCPManager
		wantErr error
	}{
		{
			name:    "nil manager",
			mcp:     nil,
			wantErr: ErrNotImplemented,
		},
		{
			name: "with manager",
			mcp: &mockMCPManager{
				tools: []MCPToolInfo{{Name: "tool1"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MonitorPlugin{mcp: tt.mcp}
			tools, err := p.ListMCPTools(context.Background())
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, tools)
		})
	}
}

func TestMonitorPlugin_CallMCPTool(t *testing.T) {
	tests := []struct {
		name    string
		mcp     MCPManager
		wantErr error
	}{
		{
			name:    "nil manager",
			mcp:     nil,
			wantErr: ErrNotImplemented,
		},
		{
			name: "with manager",
			mcp: &mockMCPManager{
				result: &MCPToolResult{ToolName: "tool1", Output: map[string]any{"ok": true}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MonitorPlugin{mcp: tt.mcp}
			result, err := p.CallMCPTool(context.Background(), "tool1", nil)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "tool1", result.ToolName)
		})
	}
}
