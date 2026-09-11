// Tool-related types re-exported for external consumers. Custom tools are
// the primary extension point of the SDK: define them with ToolFunc (or by
// implementing Tool), then register them via Runtime.ToolRegistry.
//
// These aliases live here so that external modules can build custom tools
// without importing internal packages (Go's internal visibility rule).
package sdk

import "github.com/Timwood0x10/ares/internal/apitools"

// Tool is the interface that every custom tool must implement.
type Tool = apitools.Tool

// ToolFunc is a convenience struct for defining a Tool from plain functions.
// Implement Name, Description, Parameters and Capabilities, and set Execute.
type ToolFunc = apitools.ToolFunc

// ToolResult is the outcome of a tool execution.
type ToolResult = apitools.Result
