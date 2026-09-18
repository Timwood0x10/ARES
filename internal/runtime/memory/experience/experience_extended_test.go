package experience

import (
	"context"
	"testing"
	"time"
)

func TestNewConflictResolver(t *testing.T) {
	c := NewConflictResolver()
	if c == nil {
		t.Fatal("NewConflictResolver returned nil")
	}
	if c.problemSimilarityThreshold != 0.9 {
		t.Errorf("threshold = %v, want 0.9", c.problemSimilarityThreshold)
	}
}

func TestConflictResolverConfigure(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
		wantErr   bool
	}{
		{"valid_low", 0.1, false},
		{"valid_mid", 0.5, false},
		{"valid_high", 1.0, false},
		{"zero", 0.0, true},
		{"negative", -0.5, true},
		{"above_one", 1.5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewConflictResolver()
			err := c.Configure(tt.threshold)
			if (err != nil) != tt.wantErr {
				t.Errorf("Configure(%v) err = %v, wantErr %v", tt.threshold, err, tt.wantErr)
			}
			if !tt.wantErr && c.problemSimilarityThreshold != tt.threshold {
				t.Errorf("threshold = %v, want %v", c.problemSimilarityThreshold, tt.threshold)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	c := NewConflictResolver()
	tests := []struct {
		name string
		v1   []float64
		v2   []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1.0},
		{"orthogonal", []float64{1, 0}, []float64{0, 1}, 0.0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1.0},
		{"partial", []float64{1, 1}, []float64{1, 0}, 0.7071},
		{"dim_mismatch", []float64{1, 0}, []float64{1, 0, 0}, 0.0},
		{"zero_vec1", []float64{0, 0}, []float64{1, 1}, 0.0},
		{"zero_vec2", []float64{1, 1}, []float64{0, 0}, 0.0},
		{"both_zero", []float64{0, 0}, []float64{0, 0}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.cosineSimilarity(tt.v1, tt.v2)
			if !almostEqualFloat(got, tt.want, 0.001) {
				t.Errorf("cosineSimilarity(%v, %v) = %v, want %v", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func TestDetectConflictGroups(t *testing.T) {
	c := NewConflictResolver()
	_ = c.Configure(0.8)
	ctx := context.Background()

	similar1 := &Experience{ID: "e1", Embedding: []float64{1, 0, 0}}
	similar2 := &Experience{ID: "e2", Embedding: []float64{0.99, 0.01, 0}}
	dissimilar := &Experience{ID: "e3", Embedding: []float64{0, 0, 1}}

	tests := []struct {
		name        string
		experiences []*Experience
		wantGroups  int
	}{
		{"empty", nil, 1},
		{"single", []*Experience{similar1}, 1},
		{"two_similar", []*Experience{similar1, similar2}, 1},
		{"two_dissimilar", []*Experience{similar1, dissimilar}, 2},
		{"mixed", []*Experience{similar1, similar2, dissimilar}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groups := c.DetectConflictGroups(ctx, tt.experiences)
			if len(groups) != tt.wantGroups {
				t.Errorf("got %d groups, want %d", len(groups), tt.wantGroups)
			}
		})
	}
}

func TestConflictResolverResolve(t *testing.T) {
	c := NewConflictResolver()
	_ = c.Configure(0.8)
	ctx := context.Background()

	exp1 := &Experience{ID: "e1", Embedding: []float64{1, 0, 0}}
	exp2 := &Experience{ID: "e2", Embedding: []float64{0.99, 0.01, 0}}
	exp3 := &Experience{ID: "e3", Embedding: []float64{0, 0, 1}}

	ranked := []*RankedExperience{
		{Experience: exp1, FinalScore: 0.5},
		{Experience: exp2, FinalScore: 0.9},
		{Experience: exp3, FinalScore: 0.7},
	}

	resolved := c.Resolve(ctx, ranked)
	if len(resolved) != 2 {
		t.Fatalf("resolved = %d, want 2 (one per group)", len(resolved))
	}

	// e2 should win over e1 (higher score, same group).
	foundE2 := false
	for _, r := range resolved {
		if r.ID == "e2" {
			foundE2 = true
		}
	}
	if !foundE2 {
		t.Error("e2 (higher score) should be in resolved output")
	}

	// Verify conflict markers.
	for _, r := range ranked {
		if !r.ConflictChecked {
			t.Errorf("experience %s not marked ConflictChecked", r.Experience.ID)
		}
	}
}

func TestConflictResolverResolveSingle(t *testing.T) {
	c := NewConflictResolver()
	ctx := context.Background()
	exp := &Experience{ID: "e1", Embedding: []float64{1, 0}}
	ranked := []*RankedExperience{{Experience: exp, FinalScore: 0.5}}

	resolved := c.Resolve(ctx, ranked)
	if len(resolved) != 1 {
		t.Fatalf("resolved = %d, want 1", len(resolved))
	}
	if resolved[0].ID != "e1" {
		t.Errorf("resolved ID = %q, want e1", resolved[0].ID)
	}
}

func TestFindBestInGroup(t *testing.T) {
	c := NewConflictResolver()
	exp1 := &Experience{ID: "e1"}
	exp2 := &Experience{ID: "e2"}
	exp3 := &Experience{ID: "e3"}

	group := []*Experience{exp1, exp2, exp3}
	ranked := []*RankedExperience{
		{Experience: exp1, FinalScore: 0.3},
		{Experience: exp2, FinalScore: 0.8},
		{Experience: exp3, FinalScore: 0.5},
	}

	best := c.findBestInGroup(group, ranked)
	if best.ID != "e2" {
		t.Errorf("best = %q, want e2", best.ID)
	}
}

func TestGetScoreNotFound(t *testing.T) {
	c := NewConflictResolver()
	exp := &Experience{ID: "unknown"}
	ranked := []*RankedExperience{
		{Experience: &Experience{ID: "other"}, FinalScore: 0.5},
	}
	score := c.getScore(exp, ranked)
	if score != 0.0 {
		t.Errorf("score = %v, want 0.0 for not-found", score)
	}
}

func TestExtractExperiences(t *testing.T) {
	c := NewConflictResolver()
	ranked := []*RankedExperience{
		{Experience: &Experience{ID: "e1"}},
		{Experience: &Experience{ID: "e2"}},
	}
	exps := c.extractExperiences(ranked)
	if len(exps) != 2 {
		t.Fatalf("len = %d, want 2", len(exps))
	}
	if exps[0].ID != "e1" || exps[1].ID != "e2" {
		t.Errorf("IDs = [%s, %s], want [e1, e2]", exps[0].ID, exps[1].ID)
	}
}

// ── RankingService ──────────────────────────────────────────────────────────

func TestNewRankingService(t *testing.T) {
	s := NewRankingService()
	if s == nil {
		t.Fatal("NewRankingService returned nil")
	}
	if s.usageWeight != 0.05 || s.recencyWeight != 0.05 || s.recencyDays != 30.0 {
		t.Errorf("defaults = (%v, %v, %v), want (0.05, 0.05, 30.0)",
			s.usageWeight, s.recencyWeight, s.recencyDays)
	}
}

func TestRankingServiceConfigure(t *testing.T) {
	tests := []struct {
		name    string
		weights *RankingWeights
		wantErr bool
	}{
		{"nil", nil, false},
		{"valid", &RankingWeights{UsageWeight: 0.1, RecencyWeight: 0.2, RecencyDays: 15}, false},
		{"usage_too_high", &RankingWeights{UsageWeight: 1.5, RecencyWeight: 0.1, RecencyDays: 15}, true},
		{"usage_negative", &RankingWeights{UsageWeight: -0.1, RecencyWeight: 0.1, RecencyDays: 15}, true},
		{"recency_too_high", &RankingWeights{UsageWeight: 0.1, RecencyWeight: 1.5, RecencyDays: 15}, true},
		{"recency_days_zero", &RankingWeights{UsageWeight: 0.1, RecencyWeight: 0.1, RecencyDays: 0}, true},
		{"recency_days_negative", &RankingWeights{UsageWeight: 0.1, RecencyWeight: 0.1, RecencyDays: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewRankingService()
			err := s.Configure(tt.weights)
			if (err != nil) != tt.wantErr {
				t.Errorf("Configure(%+v) err = %v, wantErr %v", tt.weights, err, tt.wantErr)
			}
		})
	}
}

func TestRankingServiceRank(t *testing.T) {
	s := NewRankingService()
	ctx := context.Background()

	now := time.Now()
	exps := []*Experience{
		{ID: "e1", UsageCount: 10, CreatedAt: now, Score: 0.0},
		{ID: "e2", UsageCount: 0, CreatedAt: now.Add(-720 * time.Hour), Score: 0.0},
		{ID: "e3", UsageCount: 5, CreatedAt: now, Score: 0.5},
	}
	scores := []float64{0.5, 0.5, 0.5}

	ranked, err := s.Rank(ctx, exps, scores)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(ranked) != 3 {
		t.Fatalf("ranked len = %d, want 3", len(ranked))
	}

	// e3 has highest score (semantic 0.5 + usage boost + recency + score 0.5).
	if ranked[0].Experience.ID != "e3" {
		t.Errorf("ranked[0] = %q, want e3", ranked[0].Experience.ID)
	}

	// Verify score components are populated.
	for _, r := range ranked {
		if r.UsageBoost < 0 {
			t.Errorf("UsageBoost = %v, want >= 0", r.UsageBoost)
		}
		if r.RecencyBoost < 0 {
			t.Errorf("RecencyBoost = %v, want >= 0", r.RecencyBoost)
		}
	}
}

func TestRankingServiceRankEmpty(t *testing.T) {
	s := NewRankingService()
	ranked, err := s.Rank(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	if len(ranked) != 0 {
		t.Errorf("ranked len = %d, want 0", len(ranked))
	}
}

func TestRankingServiceRankMismatch(t *testing.T) {
	s := NewRankingService()
	_, err := s.Rank(context.Background(), []*Experience{{ID: "e1"}}, []float64{0.5, 0.6})
	if err == nil {
		t.Error("expected error for length mismatch")
	}
}

func TestCalculateUsageBoost(t *testing.T) {
	s := NewRankingService()
	tests := []struct {
		name        string
		usageCount  int
		usageWeight float64
		wantZero    bool
		wantMax     bool
	}{
		{"zero_count", 0, 0.05, true, false},
		{"negative_count", -1, 0.05, true, false},
		{"small_count", 1, 0.05, false, false},
		{"large_count_capped", 10000, 0.5, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boost := s.calculateUsageBoost(tt.usageCount, tt.usageWeight)
			if tt.wantZero && boost != 0.0 {
				t.Errorf("boost = %v, want 0", boost)
			}
			if tt.wantMax && boost != 0.2 {
				t.Errorf("boost = %v, want capped at 0.2", boost)
			}
			if !tt.wantZero && !tt.wantMax && boost <= 0 {
				t.Errorf("boost = %v, want > 0", boost)
			}
		})
	}
}

func TestCalculateRecencyBoost(t *testing.T) {
	s := NewRankingService()
	now := time.Now()

	tests := []struct {
		name      string
		createdAt time.Time
		wantZero  bool
	}{
		{"zero_time", time.Time{}, true},
		{"recent", now, false},
		{"old", now.Add(-365 * 24 * time.Hour), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			boost := s.calculateRecencyBoost(tt.createdAt, now, 0.05, 30.0)
			if tt.wantZero && boost != 0.0 {
				t.Errorf("boost = %v, want 0", boost)
			}
			if !tt.wantZero && boost < 0 {
				t.Errorf("boost = %v, want >= 0", boost)
			}
		})
	}

	// Recent should have higher boost than old.
	recentBoost := s.calculateRecencyBoost(now, now, 0.05, 30.0)
	oldBoost := s.calculateRecencyBoost(now.Add(-365*24*time.Hour), now, 0.05, 30.0)
	if recentBoost <= oldBoost {
		t.Errorf("recentBoost %v should be > oldBoost %v", recentBoost, oldBoost)
	}
}

func TestDefaultRankingWeights(t *testing.T) {
	w := DefaultRankingWeights()
	if w.UsageWeight != 0.05 || w.RecencyWeight != 0.05 || w.RecencyDays != 30.0 {
		t.Errorf("defaults = (%v, %v, %v), want (0.05, 0.05, 30.0)",
			w.UsageWeight, w.RecencyWeight, w.RecencyDays)
	}
}

// ── Experience ──────────────────────────────────────────────────────────────

func TestExperienceGetUsageCount(t *testing.T) {
	tests := []struct {
		count int
		want  int
	}{
		{0, 0},
		{5, 5},
		{100, 100},
	}
	for _, tt := range tests {
		e := &Experience{UsageCount: tt.count}
		if got := e.GetUsageCount(); got != tt.want {
			t.Errorf("GetUsageCount() = %d, want %d", got, tt.want)
		}
	}
}

func almostEqualFloat(a, b, eps float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < eps
}
