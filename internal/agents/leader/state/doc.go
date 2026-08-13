// Package state provides persistence and recovery primitives for the leader agent.
//
// This package consolidates checkpoint storage, stale-task recovery, and
// event-based state reconstruction into a single cohesive module. The leader
// agent holds references to these types via dependency injection rather than
// managing persistence details directly.
package state
