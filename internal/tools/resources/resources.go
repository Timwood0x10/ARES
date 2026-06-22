package resources

import (
	"goagentx/internal/tools/resources/agent"
	"goagentx/internal/tools/resources/base"
	"goagentx/internal/tools/resources/core"
	"goagentx/internal/tools/resources/formatter"
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
	GlobalRegistry         = core.GlobalRegistry
	Register               = core.Register
	Get                    = core.Get
	List                   = core.List
	Execute                = core.Execute
	ErrNilTool             = core.ErrNilTool
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
