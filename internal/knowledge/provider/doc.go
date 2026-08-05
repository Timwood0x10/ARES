// Package provider defines the GraphProvider interface for pluggable
// external data sources. Every data source (PostgreSQL, MySQL, Git, Memory,
// Code, etc.) can be adapted to AKF by implementing GraphProvider.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package provider
