package resources

import (
	"github.com/Timwood0x10/ares/internal/tools/resources/agent"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/tools/resources/formatter"
)

// Core types
type (
	Tool             = core.Tool
	Capability       = core.Capability
	CapabilityEngine = core.CapabilityEngine
	Registry         = core.Registry
	Result           = core.Result
	ToolSchema       = core.ToolSchema
	ToolCategory     = core.ToolCategory
	ParameterSchema  = core.ParameterSchema
	Parameter        = core.Parameter
	ToolFilter       = core.ToolFilter
	ToolMetadata     = core.ToolMetadata
)

// Base types
type (
	BaseTool = base.BaseTool
	ToolFunc = base.ToolFunc
)

// Agent types
type (
	AgentToolConfig       = agent.AgentToolConfig
	AgentTools            = agent.AgentTools
	AgentCapabilityExport = agent.AgentCapabilityExport
)

// Formatter types
type (
	ResultFormatter = formatter.ResultFormatter
)

// Constants
const (
	CapabilityMath      = core.CapabilityMath
	CapabilityKnowledge = core.CapabilityKnowledge
	CapabilityMemory    = core.CapabilityMemory
	CapabilityText      = core.CapabilityText
	CapabilityNetwork   = core.CapabilityNetwork
	CapabilityTime      = core.CapabilityTime
	CapabilityFile      = core.CapabilityFile
	CapabilityExternal  = core.CapabilityExternal

	CategorySystem    = core.CategorySystem
	CategoryCore      = core.CategoryCore
	CategoryData      = core.CategoryData
	CategoryKnowledge = core.CategoryKnowledge
	CategoryMemory    = core.CategoryMemory
	CategoryExternal  = core.CategoryExternal
)

// Core functions
var (
	NewRegistry            = core.NewRegistry
	NewCapabilityEngine    = core.NewCapabilityEngine
	NewToolGroup           = core.NewToolGroup
	NewResult              = core.NewResult
	NewErrorResult         = core.NewErrorResult
	NewErrorResultWithCode = core.NewErrorResultWithCode
	NewValidationError     = core.NewValidationError
	ResultWithTiming       = core.ResultWithTiming
	NewResultList          = core.NewResultList
	// Deprecated: GlobalRegistry aliases core.GlobalRegistry, which is no
	// longer populated by production code after the P2.1 DI change. Use a
	// *Registry instance created via NewRegistry and passed through dependency
	// injection instead.
	GlobalRegistry = core.GlobalRegistry
	// Deprecated: Register operates on the empty GlobalRegistry. Use a
	// *Registry instance created via NewRegistry and passed through dependency
	// injection instead.
	Register = core.Register
	// Deprecated: Get operates on the empty GlobalRegistry. Use a *Registry
	// instance created via NewRegistry and passed through dependency injection
	// instead.
	Get = core.Get
	// Deprecated: List operates on the empty GlobalRegistry. Use a *Registry
	// instance created via NewRegistry and passed through dependency injection
	// instead.
	List = core.List
	// Deprecated: Execute operates on the empty GlobalRegistry. Use a
	// *Registry instance created via NewRegistry and passed through dependency
	// injection instead.
	Execute    = core.Execute
	ErrNilTool = core.ErrNilTool
)

// Base functions
var (
	NewBaseTool                 = base.NewBaseTool
	NewBaseToolWithCategory     = base.NewBaseToolWithCategory
	NewBaseToolWithCapabilities = base.NewBaseToolWithCapabilities
	NewToolFunc                 = base.NewToolFunc
	WithMetadata                = base.WithMetadata
)

// Agent functions
var (
	DefaultAgentToolConfig = agent.DefaultAgentToolConfig
	NewAgentTools          = agent.NewAgentTools
)

// Agent tool config presets
var CreateAgentToolConfigs = agent.CreateAgentToolConfigs

// Formatter functions
var (
	NewResultFormatter = formatter.NewResultFormatter
)
