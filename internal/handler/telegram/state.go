package telegram

import (
	"sync"
	"time"
)

// UserState represents the current state of a user's conversation
type UserState struct {
	UserID      int64
	CurrentStep Step
	Data        map[string]interface{}
	LastUpdated time.Time
}

// Step represents the current step in the conversation flow
type Step int

const (
	StepNone Step = iota
	StepSelectZoneForList
	StepSelectZoneForCreate
	StepSelectZoneForManage
	StepSelectRecordType
	StepInputRecordName
	StepInputRecordContent
	StepInputRecordTTL
	StepInputRecordProxied
	StepConfirmCreate
	StepSelectRecordForEdit
	StepSelectRecordForDelete
	StepEditRecordContent
	StepEditRecordTTL
	StepEditRecordProxied
	StepConfirmDelete
)

// StateManager manages user states
type StateManager struct {
	states map[int64]*UserState
	mu     sync.RWMutex
}

// NewStateManager creates a new state manager
func NewStateManager() *StateManager {
	sm := &StateManager{
		states: make(map[int64]*UserState),
	}
	// Start cleanup goroutine
	go sm.cleanup()
	return sm
}

// GetState gets or creates a user state
func (sm *StateManager) GetState(userID int64) *UserState {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if state, exists := sm.states[userID]; exists {
		state.LastUpdated = time.Now()
		return state
	}

	state := &UserState{
		UserID:      userID,
		CurrentStep: StepNone,
		Data:        make(map[string]interface{}),
		LastUpdated: time.Now(),
	}
	sm.states[userID] = state
	return state
}

// SetStep sets the current step for a user
func (sm *StateManager) SetStep(userID int64, step Step) {
	state := sm.GetState(userID)
	state.CurrentStep = step
}

// SetData sets data for a user
func (sm *StateManager) SetData(userID int64, key string, value interface{}) {
	state := sm.GetState(userID)
	state.Data[key] = value
}

// GetData gets data for a user
func (sm *StateManager) GetData(userID int64, key string) (interface{}, bool) {
	state := sm.GetState(userID)
	val, exists := state.Data[key]
	return val, exists
}

// ClearState clears a user's state
func (sm *StateManager) ClearState(userID int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.states, userID)
}

// GetCurrentStep gets the current step for a user
func (sm *StateManager) GetCurrentStep(userID int64) Step {
	state := sm.GetState(userID)
	return state.CurrentStep
}

// cleanup removes old states periodically
func (sm *StateManager) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		for userID, state := range sm.states {
			if now.Sub(state.LastUpdated) > 30*time.Minute {
				delete(sm.states, userID)
			}
		}
		sm.mu.Unlock()
	}
}
