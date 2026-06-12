package context

import (
	"context"
	"errors"
	"sync"
	"time"

	"goagentx/internal/core/models"
)

// Memory errors.
var (
	ErrUserNotFound = errors.New("user not found")
)

// UserMemory stores long-term user preferences and history.
type UserMemory struct {
	users   map[string]*UserData
	mu      sync.RWMutex
	maxSize int
}

// UserData holds user information.
type UserData struct {
	UserID         string
	Profile        *models.UserProfile
	Preferences    []Preference
	History        []Interaction
	StyleEvolution []StyleEntry
	LastUpdated    time.Time
	CreatedAt      time.Time
}

// Preference represents a user preference.
type Preference struct {
	Category  string    `json:"category"`
	Value     string    `json:"value"`
	Score     float64   `json:"score"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Interaction represents a user interaction.
type Interaction struct {
	Type      string                 `json:"type"`
	SessionID string                 `json:"session_id"`
	Items     []string               `json:"items"`
	Feedback  string                 `json:"feedback"`
	Time      time.Time              `json:"time"`
	Metadata  map[string]interface{} `json:"metadata"`
}

// StyleEntry represents a style preference change over time.
type StyleEntry struct {
	Style      []string  `json:"style"`
	Occasion   string    `json:"occasion"`
	Confidence float64   `json:"confidence"`
	Time       time.Time `json:"time"`
}

// NewUserMemory creates a new UserMemory.
func NewUserMemory(maxSize int) *UserMemory {
	return &UserMemory{
		users:   make(map[string]*UserData),
		maxSize: maxSize,
	}
}

// Get retrieves user data.
func (m *UserMemory) Get(ctx context.Context, userID string) (*UserData, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	return user, exists
}

// Set stores user data.
func (m *UserMemory) Set(ctx context.Context, userID string, profile *models.UserProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.users) >= m.maxSize {
		m.evictLeastUsed()
	}

	if existing, exists := m.users[userID]; exists {
		existing.Profile = profile
		existing.LastUpdated = time.Now()
		m.users[userID] = existing
		return nil
	}

	user := &UserData{
		UserID:         userID,
		Profile:        profile,
		LastUpdated:    time.Now(),
		CreatedAt:      time.Now(),
		Preferences:    make([]Preference, 0),
		History:        make([]Interaction, 0),
		StyleEvolution: make([]StyleEntry, 0),
	}

	m.users[userID] = user
	return nil
}

// UpdateProfile updates user profile.
func (m *UserMemory) UpdateProfile(ctx context.Context, userID string, profile *models.UserProfile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return ErrUserNotFound
	}

	user.Profile = profile
	user.LastUpdated = time.Now()

	return nil
}

// AddPreference adds a user preference.
func (m *UserMemory) AddPreference(ctx context.Context, userID string, pref Preference) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return ErrUserNotFound
	}

	pref.UpdatedAt = time.Now()
	user.Preferences = append(user.Preferences, pref)
	user.LastUpdated = time.Now()

	return nil
}

// GetPreferences returns user preferences.
func (m *UserMemory) GetPreferences(ctx context.Context, userID string) ([]Preference, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	if !exists {
		return nil, ErrUserNotFound
	}

	result := make([]Preference, len(user.Preferences))
	copy(result, user.Preferences)
	return result, nil
}

// AddInteraction adds a user interaction.
func (m *UserMemory) AddInteraction(ctx context.Context, userID string, interaction Interaction) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return ErrUserNotFound
	}

	interaction.Time = time.Now()
	user.History = append(user.History, interaction)
	user.LastUpdated = time.Now()

	// Keep only last 1000 interactions
	if len(user.History) > 1000 {
		user.History = user.History[len(user.History)-1000:]
	}

	return nil
}

// GetHistory returns user interaction history.
func (m *UserMemory) GetHistory(ctx context.Context, userID string) ([]Interaction, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	if !exists {
		return nil, ErrUserNotFound
	}

	result := make([]Interaction, len(user.History))
	copy(result, user.History)
	return result, nil
}

// UpdateStyleEvolution updates style preference evolution.
func (m *UserMemory) UpdateStyleEvolution(ctx context.Context, userID string, entry StyleEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	user, exists := m.users[userID]
	if !exists {
		return ErrUserNotFound
	}

	entry.Time = time.Now()
	user.StyleEvolution = append(user.StyleEvolution, entry)

	// Keep only last 100 entries
	if len(user.StyleEvolution) > 100 {
		user.StyleEvolution = user.StyleEvolution[len(user.StyleEvolution)-100:]
	}

	return nil
}

// GetStyleEvolution returns style preference evolution.
func (m *UserMemory) GetStyleEvolution(ctx context.Context, userID string) ([]StyleEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, exists := m.users[userID]
	if !exists {
		return nil, ErrUserNotFound
	}

	result := make([]StyleEntry, len(user.StyleEvolution))
	copy(result, user.StyleEvolution)
	return result, nil
}

// Delete removes user data.
func (m *UserMemory) Delete(ctx context.Context, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.users, userID)
	return nil
}

// Size returns the number of users.
func (m *UserMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.users)
}

// evictLeastUsed removes the least recently used user.
func (m *UserMemory) evictLeastUsed() {
	var oldest *UserData
	var oldestID string

	for id, user := range m.users {
		if oldest == nil || user.LastUpdated.Before(oldest.LastUpdated) {
			oldest = user
			oldestID = id
		}
	}

	if oldestID != "" {
		delete(m.users, oldestID)
	}
}
