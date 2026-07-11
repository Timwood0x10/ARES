// Package services provides retrieval services for the storage system.
package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// shouldRewriteQuery determines if a query should be rewritten.
func (s *RetrievalService) shouldRewriteQuery(query string) bool {
	// Skip very short queries (byte-level check).
	// Note: isPrecisionMode uses rune count for different semantics (precision trigger).
	// Here we skip only trivially short inputs that cannot benefit from rewriting.
	if len(query) < 10 {
		return false
	}

	// Skip if query is in cache (simple check)
	if s.isQueryInCache(query) {
		return false
	}

	// Complex query patterns that benefit from rewriting
	complexPatterns := []string{
		"如何", "怎么", "什么", "why", "为什么",
		"what", "how", "explain", "解释", "describe", "描述",
	}

	for _, pattern := range complexPatterns {
		if contains(query, pattern) {
			return true
		}
	}

	return false
}

// isQueryInCache checks if query results are already cached within TTL.
// Uses RLock for the read path to minimize contention; expired entry cleanup
// is deferred to markQueryCached to avoid write lock overhead on read-heavy paths.
func (s *RetrievalService) isQueryInCache(query string) bool {
	if len(query) == 0 {
		return false
	}

	normalized := s.normalizeQueryForCache(query)

	s.queryCacheMu.RLock()
	defer s.queryCacheMu.RUnlock()

	cachedAt, exists := s.queryCache[normalized]
	if !exists {
		return false
	}

	// Expired entries are lazily cleaned up by markQueryCached.
	// Here we simply report the cache status.
	return time.Since(cachedAt) <= s.queryCacheTTL
}

// markQueryCached records a query as processed to skip future rewrites.
// Thread-safe: acquires write lock for the entire operation.
// Eviction strategy: first remove expired entries, then remove oldest if still at capacity.
func (s *RetrievalService) markQueryCached(query string) {
	if len(query) == 0 {
		return
	}

	normalized := s.normalizeQueryForCache(query)

	s.queryCacheMu.Lock()
	defer s.queryCacheMu.Unlock()

	// Evict expired entries if cache is at capacity.
	if len(s.queryCache) >= s.queryCacheMaxLen {
		now := time.Now()
		for key, ts := range s.queryCache {
			if now.Sub(ts) > s.queryCacheTTL {
				delete(s.queryCache, key)
			}
		}
	}

	// Fallback: if still at capacity after expired eviction, remove oldest entry.
	if len(s.queryCache) >= s.queryCacheMaxLen {
		var oldestKey string
		var oldestTime time.Time
		for key, ts := range s.queryCache {
			if oldestKey == "" || ts.Before(oldestTime) {
				oldestKey = key
				oldestTime = ts
			}
		}
		if oldestKey != "" {
			delete(s.queryCache, oldestKey)
		}
	}

	s.queryCache[normalized] = time.Now()
}

// normalizeQueryForCache normalizes query text for cache key usage.
// Strips whitespace and converts to lowercase for case-insensitive matching.
func (s *RetrievalService) normalizeQueryForCache(query string) string {
	trimmed := strings.TrimSpace(query)
	return toLower(trimmed)
}

// queryRewrite rewrites a query for better retrieval.
// This uses LLM to expand and refine the query.
func (s *RetrievalService) queryRewrite(ctx context.Context, query string) (string, error) {
	// Use LLM-based rewrite for backward compatibility
	rewrites, err := s.llmBasedRewrite(ctx, query)
	if err != nil {
		s.logger.Warn("LLM rewrite failed, returning original query", "error", err)
		return query, nil
	}

	// Return the best rewrite or original
	if len(rewrites) > 0 {
		return rewrites[0], nil
	}

	return query, nil
}

// buildQueries constructs a list of weighted queries based on the original query and rewrites.
// This implements the converged version with weight control to prevent rewrites from dominating.
// Args:
// ctx - operation context.
// original - original query text.
// plan - retrieval plan with rewrite configuration.
// Returns list of weighted queries ordered by priority.
func (s *RetrievalService) buildQueries(ctx context.Context, original string, plan *RetrievalPlan) []WeightedQuery {
	queries := []WeightedQuery{
		{Query: original, Weight: s.queryPriority.OriginalWeight, Source: "original"},
	}

	// 1. Rule-based rewriting (low cost, stable)
	if plan.EnableQueryRewrite {
		ruleRewrites := s.ruleBasedRewrite(original)

		for _, r := range ruleRewrites {
			queries = append(queries, WeightedQuery{
				Query:  r,
				Weight: s.queryPriority.RuleRewriteWeight,
				Source: "rewrite_rule",
			})
		}
	}

	// 2. LLM-based rewriting (optional, high quality but lower weight + fail-safe)
	if plan.EnableQueryRewrite {
		llmRewrites, err := s.llmBasedRewrite(ctx, original)
		if err != nil {
			s.logger.Warn("LLM rewrite failed, using rule-based only", "error", err)
		} else {
			// Validate rewrite quality
			validated := s.validateRewrites(original, llmRewrites)

			// Deduplicate
			uniqueRewrites := s.uniqueRewrites(validated)

			// Limit count (critical to prevent explosion, max 2)
			maxLLMRewrites := 2
			if len(uniqueRewrites) > maxLLMRewrites {
				uniqueRewrites = uniqueRewrites[:maxLLMRewrites]
			}

			for _, r := range uniqueRewrites {
				queries = append(queries, WeightedQuery{
					Query:  r,
					Weight: s.queryPriority.LLMRewriteWeight,
					Source: "rewrite_llm",
				})
			}
		}
	}

	// 3. Limit total count (critical to prevent explosion)
	maxQueries := s.queryPriority.MaxQueries
	if len(queries) > maxQueries {
		queries = queries[:maxQueries]
	}

	return queries
}

// loadSynonymRules loads synonym rules from configuration file.
// This provides better maintainability and allows runtime configuration.
// Returns map of original terms to their synonyms.
// Uses CONFIG_PATH environment variable if set, otherwise uses relative path.
func loadSynonymRules() map[string][]string {
	// Default rules if config file not found
	defaultRules := map[string][]string{
		"how to":   {"how do i", "what is the best way to", "how can i"},
		"what is":  {"define", "explain", "describe"},
		"编程":       {"开发", "写代码", "编码", "程序设计"},
		"并发":       {"并行", "多线程", "异步"},
		"database": {"db", "data storage"},
		"api":      {"interface", "web service"},
	}

	// Use environment variable if set, otherwise fall back to relative path
	configPath := os.Getenv("SYNONYM_CONFIG_PATH")
	if configPath == "" {
		// Try to get the absolute path based on executable location
		execPath, err := os.Executable()
		if err == nil {
			configPath = filepath.Join(filepath.Dir(execPath), "..", "..", "configs", "synonyms.yaml")
		} else {
			configPath = "configs/synonyms.yaml"
		}
	}

	// Security: validate path is within allowed directory
	if dir := getAllowedSynonymDir(); dir != "" {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return defaultRules
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return defaultRules
		}
		if !strings.HasPrefix(absPath, absDir) {
			return defaultRules
		}
	}

	if _, err := os.Stat(configPath); err != nil { // #nosec G703
		return defaultRules
	}

	data, err := os.ReadFile(configPath) // #nosec G304, G703
	if err != nil {
		return defaultRules
	}

	// Parse YAML config
	var config map[string][]string
	if err := yaml.Unmarshal(data, &config); err != nil {
		return defaultRules
	}

	return config
}

// ruleBasedRewrite performs rule-based query rewriting.
// This uses predefined rules for query expansion without LLM overhead.
// Args:
// original - original query text.
// Returns list of rewritten queries.
func (s *RetrievalService) ruleBasedRewrite(original string) []string {
	rewrites := []string{}

	// Normalize English queries (expand contractions, standardize format)
	normalized := normalizeEnglishQuery(original)

	// Use synonym rules loaded from configuration file
	queryLower := toLower(normalized)
	for key, synonyms := range s.synonymRules {
		if contains(queryLower, key) {
			for _, synonym := range synonyms {
				rewrites = append(rewrites, replaceCaseInsensitive(normalized, key, synonym))
			}
		}
	}

	return rewrites
}

// validateRewrites validates the quality of rewritten queries.
// This filters out rewrites that are too different or malformed.
// Args:
// original - original query text.
// rewrites - list of rewritten queries.
// Returns list of valid rewrites.
func (s *RetrievalService) validateRewrites(original string, rewrites []string) []string {
	valid := []string{}

	for _, r := range rewrites {
		// Rule 1: Similarity to original cannot be too low
		if s.calculateSimilarity(original, r) < 0.6 {
			s.logger.Debug("Rewrite too different from original", "original", original, "rewrite", r)
			continue
		}

		// Rule 2: Length cannot exceed 2x original
		if len(r) > 2*len(original) {
			s.logger.Debug("Rewrite too long", "original", original, "rewrite", r)
			continue
		}

		// Rule 3: Cannot be empty
		if r == "" {
			continue
		}

		valid = append(valid, r)
	}

	return valid
}

// uniqueRewrites removes duplicate queries from the list.
// Args:
// rewrites - list of rewritten queries.
// Returns list of unique queries.
func (s *RetrievalService) uniqueRewrites(rewrites []string) []string {
	seen := make(map[string]bool)
	unique := []string{}

	for _, r := range rewrites {
		if !seen[r] {
			seen[r] = true
			unique = append(unique, r)
		}
	}

	return unique
}

// llmBasedRewrite performs LLM-based query rewriting.
// This uses LLM to generate high-quality query variations.
// Args:
// ctx - operation context.
// query - original query text.
// Returns list of rewritten queries or error.
func (s *RetrievalService) llmBasedRewrite(ctx context.Context, query string) ([]string, error) {
	// Check if LLM client is available and enabled
	if s.llmClient == nil || !s.llmClient.IsEnabled() {
		s.logger.Debug("LLM client not available or disabled, skipping LLM rewrite")
		return []string{}, nil
	}

	// Build prompt for query rewriting
	prompt := fmt.Sprintf(`You are a search query optimization assistant. Your task is to rewrite the given search query to improve retrieval results.

Rules:
1. Keep the original intent but use different wording
2. Generate up to 3 alternative queries
3. Return each query on a separate line
4. Be concise and clear
5. Focus on semantic similarity rather than exact matches

Original Query: %s

Rewritten Queries (one per line):`, query)

	// Call LLM API with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	response, err := s.llmClient.Generate(timeoutCtx, prompt)
	if err != nil {
		s.logger.Warn("LLM rewrite failed", "error", err, "provider", s.llmClient.GetProvider())
		return []string{}, nil // Don't fail the whole process, just return empty
	}

	// Parse response into individual queries
	rewrites := s.parseLLMResponse(response)

	s.logger.Info("LLM rewrite completed", "original", query, "rewrites_count", len(rewrites), "provider", s.llmClient.GetProvider())

	return rewrites, nil
}

// parseLLMResponse parses LLM response into individual query lines.
// Args:
// response - LLM response text.
// Returns list of parsed queries.
func (s *RetrievalService) parseLLMResponse(response string) []string {
	queries := []string{}

	// Split by lines and filter empty lines
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			queries = append(queries, line)
		}
	}

	// Limit to 3 queries
	if len(queries) > 3 {
		queries = queries[:3]
	}

	return queries
}

// calculateSimilarity calculates similarity between two strings.
// Args:
// s1 - first string.
// s2 - second string.
// Returns similarity score between 0 and 1.
func (s *RetrievalService) calculateSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	// Simple Jaccard similarity based on word overlap
	words1 := make(map[string]bool)
	words2 := make(map[string]bool)

	for _, word := range tokenize(toLower(s1)) {
		if word != "" {
			words1[word] = true
		}
	}

	for _, word := range tokenize(toLower(s2)) {
		if word != "" {
			words2[word] = true
		}
	}

	intersection := 0
	for word := range words1 {
		if words2[word] {
			intersection++
		}
	}

	union := len(words1) + len(words2) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// replaceCaseInsensitive replaces all occurrences of old substring with new string, ignoring case.
// This correctly handles multi-byte UTF-8 characters by using strings.Contains with lowercasing.
//
// Args:
// s - original string.
// old - substring to replace.
// new - replacement string.
// Returns string with replacement applied.
func replaceCaseInsensitive(s, old, new string) string {
	if old == "" {
		return s
	}

	sLower := toLower(s)
	oldLower := toLower(old)

	result := strings.Builder{}
	i := 0
	for i < len(s) {
		// Find next occurrence of old substring
		if i <= len(s)-len(old) && sLower[i:i+len(old)] == oldLower {
			result.WriteString(new)
			i += len(old)
		} else {
			// Write one rune at a time to handle multi-byte characters
			_, size := utf8.DecodeRuneInString(s[i:])
			result.WriteString(s[i : i+size])
			i += size
		}
	}

	return result.String()
}

// tokenize splits a string into words.
// Args:
// s - string to tokenize.
// Returns list of words.
func tokenize(s string) []string {
	words := []string{}
	currentWord := ""

	for _, ch := range s {
		if isWordChar(ch) {
			currentWord += string(ch)
		} else if currentWord != "" {
			words = append(words, currentWord)
			currentWord = ""
		}
	}

	if currentWord != "" {
		words = append(words, currentWord)
	}

	return words
}

// isWordChar checks if a character is a word character.
// Args:
// ch - rune to check.
// Returns true if character is alphanumeric.
func isWordChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}
