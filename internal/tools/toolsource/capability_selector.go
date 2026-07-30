package toolsource

import (
	"context"

	"github.com/Timwood0x10/ares/internal/tools/planner"
	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// CapabilityExtractor maps a user input to capability names understood by
// planner.ToolResolver. Implementations must be deterministic and side-effect
// free.
type CapabilityExtractor func(input string) []string

// DefaultCapabilityExtractor is a keyword→capability heuristic that maps
// common user-input words to planner capability names (e.g. "calculate" →
// "Arithmetic"). It returns an empty slice when no capabilities can be
// inferred. The capability names here must match keys in
// planner.capabilityMapping so the resolver can resolve them.
var DefaultCapabilityExtractor CapabilityExtractor = defaultCapabilityExtractor

// Planner capability names referenced by the keyword map below. These mirror
// the string literals in internal/tools/planner (which keeps them as literals
// by package-local goconst exemption). toolsource re-exposes the subset it
// routes to as typed constants so the map values stay maintainable and
// goconst-clean here. They MUST match planner's capability names exactly.
const (
	capArithmetic           = "Arithmetic"
	capSummation            = "Summation"
	capNumberTheory         = "NumberTheory"
	capProbability          = "Probability"
	capStatistics           = "Statistics"
	capExpressionEvaluation = "ExpressionEvaluation"
	capStringManipulation   = "StringManipulation"
	capRegex                = "Regex"
	capHashing              = "Hashing"
	capBase64               = "Base64"
	capJSONProcessing       = "JSONProcessing"
	capPDFParsing           = "PDFParsing"
	capTextExtraction       = "TextExtraction"
	capHTTPRequest          = "HTTPRequest"
	capWebFetch             = "WebFetch"
	capWebSearch            = "WebSearch"
	capIDGeneration         = "IDGeneration"
	capCodeExecution        = "CodeExecution"
	capTaskPlanning         = "TaskPlanning"
	capDateTime             = "DateTime"
	capDataTransform        = "DataTransform"
	capDataValidation       = "DataValidation"
	capLogAnalysis          = "LogAnalysis"
	capEmbedding            = "Embedding"
)

// keywordToCapability maps user-input keywords to planner capability names.
// This is a self-contained heuristic (it does NOT import planner's unexported
// broadToGranular map) reusing the broad category concept: keywords like
// "math"/"calculate" expand to the granular capability "Arithmetic" that the
// resolver's static capability mapping understands.
var keywordToCapability = map[string]string{
	// math
	"math":        capArithmetic,
	"calculate":   capArithmetic,
	"calc":        capArithmetic,
	"sum":         capSummation,
	"add":         capArithmetic,
	"subtract":    capArithmetic,
	"multiply":    capArithmetic,
	"divide":      capArithmetic,
	"number":      capNumberTheory,
	"probability": capProbability,
	"statistic":   capStatistics,
	"stats":       capStatistics,
	"expression":  capExpressionEvaluation,
	"formula":     capExpressionEvaluation,
	// text / strings
	"string":  capStringManipulation,
	"regex":   capRegex,
	"pattern": capRegex,
	"hash":    capHashing,
	"sha":     capHashing,
	"md5":     capHashing,
	"base64":  capBase64,
	"encode":  capBase64,
	"decode":  capBase64,
	"json":    capJSONProcessing,
	// files / extraction
	"pdf":     capPDFParsing,
	"extract": capTextExtraction,
	// network
	"http":     capHTTPRequest,
	"request":  capHTTPRequest,
	"url":      capHTTPRequest,
	"api":      capHTTPRequest,
	"fetch":    capWebFetch,
	"download": capWebFetch,
	"search":   capWebSearch,
	"web":      capWebSearch,
	"google":   capWebSearch,
	// system
	"id":      capIDGeneration,
	"uuid":    capIDGeneration,
	"guid":    capIDGeneration,
	"code":    capCodeExecution,
	"execute": capCodeExecution,
	"run":     capCodeExecution,
	"plan":    capTaskPlanning,
	"task":    capTaskPlanning,
	// time
	"time":      capDateTime,
	"date":      capDateTime,
	"datetime":  capDateTime,
	"timestamp": capDateTime,
	"schedule":  capDateTime,
	// data
	"transform":  capDataTransform,
	"validate":   capDataValidation,
	"validation": capDataValidation,
	"log":        capLogAnalysis,
	"logs":       capLogAnalysis,
	"embedding":  capEmbedding,
	"vector":     capEmbedding,
}

// defaultCapabilityExtractor is the DefaultCapabilityExtractor implementation.
// It reuses extractKeywords (shared with TagSelector) to tokenize input,
// then maps each keyword through keywordToCapability, collapsing duplicates.
func defaultCapabilityExtractor(input string) []string {
	keywords := extractKeywords(input)
	if len(keywords) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(keywords))
	caps := make([]string, 0, len(keywords))
	for _, kw := range keywords {
		cap, ok := keywordToCapability[kw]
		if !ok || seen[cap] {
			continue
		}
		seen[cap] = true
		caps = append(caps, cap)
	}
	return caps
}

// CapabilitySelector reuses planner.ToolResolver + planner.ToolScorer to pick
// the best tool per extracted capability. If no capabilities are extracted, or
// resolution finds nothing, all available tools are returned (graceful
// fallback so the LLM never sees an empty toolset).
type CapabilitySelector struct {
	resolver planner.ToolResolver
	scorer   planner.ToolScorer
	extract  CapabilityExtractor
}

// NewCapabilitySelector builds a CapabilitySelector. If extract is nil,
// DefaultCapabilityExtractor is used. If resolver or scorer is nil, Select
// degrades to returning all available (graceful fallback).
func NewCapabilitySelector(resolver planner.ToolResolver, scorer planner.ToolScorer, extract CapabilityExtractor) *CapabilitySelector {
	if extract == nil {
		extract = DefaultCapabilityExtractor
	}
	return &CapabilitySelector{resolver: resolver, scorer: scorer, extract: extract}
}

// Select extracts capabilities from input, resolves+scores each, picks the
// top-1 candidate per capability, and returns the matching tools from
// available. Falls back to all available when no capabilities are extracted
// or resolution finds nothing.
//
// The CapabilityRequirement is built with Name=capability only: the planner
// resolver matches on Name and ignores InputType/OutputType, and deriving
// those from available tool tags would require knowing which tool provides the
// capability (the resolver's job), so they are left empty.
func (s *CapabilitySelector) Select(ctx context.Context, input string, available []core.Tool) ([]core.Tool, error) {
	if s == nil || s.resolver == nil || s.scorer == nil || s.extract == nil {
		return available, nil
	}
	caps := s.extract(input)
	if len(caps) == 0 {
		return available, nil
	}
	byName := make(map[string]core.Tool, len(available))
	for _, t := range available {
		if t == nil {
			continue
		}
		byName[t.Name()] = t
	}
	chosen := make(map[string]bool)
	selected := make([]core.Tool, 0)
	for _, cap := range caps {
		req := &planner.CapabilityRequirement{Name: cap}
		candidates, err := s.resolver.Resolve(ctx, req)
		if err != nil || len(candidates) == 0 {
			continue
		}
		scored, err := s.scorer.Score(ctx, candidates, nil)
		if err != nil || len(scored) == 0 {
			continue
		}
		top := scored[0].ToolName
		if chosen[top] {
			continue
		}
		if tool, ok := byName[top]; ok {
			chosen[top] = true
			selected = append(selected, tool)
		}
	}
	if len(selected) == 0 {
		return available, nil
	}
	return selected, nil
}
