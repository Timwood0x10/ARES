// Package aggregate implements result aggregation for the leader agent.
//
// It combines TaskResults from multiple sub-agents into a single RecommendResult,
// with optional deduplication, priority/createdAt sorting, and item count limiting.
// The ResultAggregator interface is defined in the parent leader package (consumer side);
// this package provides the concrete implementation.
package aggregate
