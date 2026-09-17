// nolint: errcheck // Test code may ignore return values
package models

import (
	"testing"
	"time"
)

func TestGender(t *testing.T) {
	tests := []struct {
		name     string
		gender   Gender
		expected string
	}{
		{"male", GenderMale, "male"},
		{"female", GenderFemale, "female"},
		{"other", GenderOther, "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.gender) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.gender)
			}
		})
	}
}

func TestStyleTag(t *testing.T) {
	tests := []struct {
		name     string
		tag      StyleTag
		expected string
	}{
		{"casual", StyleTag("casual"), "casual"},
		{"formal", StyleTag("formal"), "formal"},
		{"street", StyleTag("street"), "street"},
		{"sporty", Sporty, "sporty"},
		{"minimalist", StyleMinimalist, "minimalist"},
		{"vintage", StyleVintage, "vintage"},
		{"bohemian", StyleBohemian, "bohemian"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.tag) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.tag)
			}
		})
	}
}

func TestOccasion(t *testing.T) {
	tests := []struct {
		name     string
		occasion Occasion
		expected string
	}{
		{"daily", Occasion("daily"), "daily"},
		{"work", OccasionWork, "work"},
		{"party", Occasion("party"), "party"},
		{"date", Occasion("date"), "date"},
		{"sports", OccasionSports, "sports"},
		{"formal", OccasionFormal, "formal"},
		{"vacation", OccasionVacation, "vacation"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.occasion) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.occasion)
			}
		})
	}
}

func TestAgentType(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		expected  string
	}{
		{"top", AgentTypeTop, "agent_top"},
		{"bottom", AgentTypeBottom, "agent_bottom"},
		{"shoes", AgentType("agent_shoes"), "agent_shoes"},
		{"head", AgentType("agent_head"), "agent_head"},
		{"accessory", AgentType("agent_accessory"), "agent_accessory"},
		// Travel agent types
		{"destination", AgentTypeDestination, "destination"},
		{"food", AgentTypeFood, "food"},
		{"hotel", AgentTypeHotel, "hotel"},
		{"itinerary", AgentTypeItinerary, "itinerary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.agentType) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.agentType)
			}
		})
	}
}

func TestSessionStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   SessionStatus
		expected string
	}{
		{"pending", SessionStatusPending, "pending"},
		{"processing", SessionStatusProcessing, "processing"},
		{"completed", SessionStatusCompleted, "completed"},
		{"failed", SessionStatusFailed, "failed"},
		{"expired", SessionStatusExpired, "expired"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.status)
			}
		})
	}
}

func TestAgentStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   AgentStatus
		expected string
	}{
		{"starting", AgentStatusStarting, "starting"},
		{"ready", AgentStatusReady, "ready"},
		{"busy", AgentStatusBusy, "busy"},
		{"stopping", AgentStatusStopping, "stopping"},
		{"offline", AgentStatusOffline, "offline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.status)
			}
		})
	}
}

func TestUserProfile(t *testing.T) {
	t.Run("create user profile", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")

		if profile.UserID != "user123" {
			t.Errorf("expected user123, got %s", profile.UserID)
		}
		if profile.Name != "John Doe" {
			t.Errorf("expected John Doe, got %s", profile.Name)
		}
		if profile.Preferences == nil {
			t.Errorf("expected preferences to be initialized")
		}
	})

	t.Run("validate valid profile", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")
		profile.Age = 25

		err := profile.Validate()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("validate invalid user id", func(t *testing.T) {
		profile := NewUserProfile("", "John Doe")

		err := profile.Validate()
		if err == nil {
			t.Errorf("expected error for empty user id")
		}
	})

	t.Run("validate invalid age", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")
		profile.Age = 200

		err := profile.Validate()
		if err == nil {
			t.Errorf("expected error for invalid age")
		}
	})

	t.Run("validate invalid age negative", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")
		profile.Age = -1

		err := profile.Validate()
		if err == nil {
			t.Errorf("expected error for negative age")
		}
	})

	t.Run("validate with budget", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")
		profile.Budget = &PriceRange{Min: 100, Max: 500}

		err := profile.Validate()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("validate with invalid budget", func(t *testing.T) {
		profile := NewUserProfile("user123", "John Doe")
		profile.Budget = &PriceRange{Min: 500, Max: 100}

		err := profile.Validate()
		if err == nil {
			t.Errorf("expected error for invalid budget")
		}
	})
}

func TestUserFeedback(t *testing.T) {
	t.Run("create user feedback", func(t *testing.T) {
		feedback := &UserFeedback{
			Liked:   true,
			Rating:  5,
			Comment: "Great style!",
		}

		if !feedback.Liked {
			t.Errorf("expected liked to be true")
		}
		if feedback.Rating != 5 {
			t.Errorf("expected rating 5, got %d", feedback.Rating)
		}
	})

	t.Run("is valid rating", func(t *testing.T) {
		feedback := &UserFeedback{Rating: 3}
		if !feedback.IsValid() {
			t.Errorf("expected valid rating")
		}
	})

	t.Run("is valid rating boundary min", func(t *testing.T) {
		feedback := &UserFeedback{Rating: 1}
		if !feedback.IsValid() {
			t.Errorf("expected valid rating 1")
		}
	})

	t.Run("is valid rating boundary max", func(t *testing.T) {
		feedback := &UserFeedback{Rating: 5}
		if !feedback.IsValid() {
			t.Errorf("expected valid rating 5")
		}
	})

	t.Run("is valid rating invalid", func(t *testing.T) {
		feedback := &UserFeedback{Rating: 0}
		if feedback.IsValid() {
			t.Errorf("expected invalid rating 0")
		}
	})

	t.Run("is valid rating too high", func(t *testing.T) {
		feedback := &UserFeedback{Rating: 6}
		if feedback.IsValid() {
			t.Errorf("expected invalid rating 6")
		}
	})
}

func TestTask(t *testing.T) {
	t.Run("create task", func(t *testing.T) {
		profile := &UserProfile{UserID: "user1"}
		task := NewTask("task123", AgentTypeTop, profile)

		if task.TaskID != "task123" {
			t.Errorf("expected task123, got %s", task.TaskID)
		}
		if task.AgentType != AgentTypeTop {
			t.Errorf("expected top agent, got %s", task.AgentType)
		}
		if task.UserProfile != profile {
			t.Errorf("expected user profile")
		}
	})

	t.Run("is expired false", func(t *testing.T) {
		task := &Task{
			Deadline: time.Now().Add(time.Hour),
		}

		if task.IsExpired() {
			t.Errorf("expected not expired")
		}
	})

	t.Run("is expired true", func(t *testing.T) {
		task := &Task{
			Deadline: time.Now().Add(-time.Hour),
		}

		if !task.IsExpired() {
			t.Errorf("expected expired")
		}
	})
}

func TestTaskResult(t *testing.T) {
	t.Run("create task result", func(t *testing.T) {
		result := NewTaskResult("task123", AgentTypeTop)

		if result.TaskID != "task123" {
			t.Errorf("expected task123, got %s", result.TaskID)
		}
		if result.AgentType != AgentTypeTop {
			t.Errorf("expected top agent, got %s", result.AgentType)
		}
	})

	t.Run("set success", func(t *testing.T) {
		result := &TaskResult{}
		items := []*RecommendItem{
			{ItemID: "item1", Name: "T-Shirt"},
		}

		result.SetSuccess(items, "Great match")

		if len(result.Items) != 1 {
			t.Errorf("expected 1 item, got %d", len(result.Items))
		}
		if result.Reason != "Great match" {
			t.Errorf("expected Great match, got %s", result.Reason)
		}
		if !result.Success {
			t.Errorf("expected success to be true")
		}
	})

	t.Run("set error", func(t *testing.T) {
		result := &TaskResult{}

		result.SetError("Something went wrong")

		if result.Error != "Something went wrong" {
			t.Errorf("expected error message, got %s", result.Error)
		}
		if result.Success {
			t.Errorf("expected success to be false")
		}
	})
}

func TestTaskContext(t *testing.T) {
	t.Run("create task context", func(t *testing.T) {
		ctx := &TaskContext{
			Dependencies: []string{"dep1", "dep2"},
			DepResults:   make(map[string]*TaskResult),
			Coordination: make(map[string]any),
		}

		if len(ctx.Dependencies) != 2 {
			t.Errorf("expected 2 dependencies, got %d", len(ctx.Dependencies))
		}
	})

	t.Run("add dependency result", func(t *testing.T) {
		ctx := &TaskContext{
			DepResults: make(map[string]*TaskResult),
		}

		result := &TaskResult{TaskID: "task1"}
		ctx.DepResults["task1"] = result

		if ctx.DepResults["task1"] != result {
			t.Errorf("expected to get dependency result")
		}
	})
}

func TestPriceRange(t *testing.T) {
	t.Run("is valid", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if !pr.IsValid() {
			t.Errorf("expected valid price range")
		}
	})

	t.Run("is valid equal", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 100}
		if !pr.IsValid() {
			t.Errorf("expected valid price range with equal min/max")
		}
	})

	t.Run("is valid with invalid range", func(t *testing.T) {
		pr := &PriceRange{Min: 500, Max: 100}
		if pr.IsValid() {
			t.Errorf("expected invalid price range")
		}
	})

	t.Run("contains within range", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if !pr.Contains(300) {
			t.Errorf("expected 300 to be within range")
		}
	})

	t.Run("contains at min", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if !pr.Contains(100) {
			t.Errorf("expected 100 to be within range")
		}
	})

	t.Run("contains at max", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if !pr.Contains(500) {
			t.Errorf("expected 500 to be within range")
		}
	})

	t.Run("contains below range", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if pr.Contains(50) {
			t.Errorf("expected 50 to be outside range")
		}
	})

	t.Run("contains above range", func(t *testing.T) {
		pr := &PriceRange{Min: 100, Max: 500}
		if pr.Contains(600) {
			t.Errorf("expected 600 to be outside range")
		}
	})
}

func TestDefaultSessionTTL(t *testing.T) {
	if DefaultSessionTTL <= 0 {
		t.Errorf("expected positive default session TTL")
	}
}
