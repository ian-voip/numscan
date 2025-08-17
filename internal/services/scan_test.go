package services

import (
	"context"
	"sync"
	"testing"

	"numscan/internal/esl"

	"github.com/percipia/eslgo"
	"github.com/stretchr/testify/mock"
)

// MockESLManager implements the ESLManager interface for testing
type MockESLManager struct {
	mock.Mock
	connected     bool
	listeners     map[string]map[string]esl.EventHandler
	eventHandlers map[string]esl.EventHandler
	mu            sync.RWMutex
}

func NewMockESLManager() *MockESLManager {
	return &MockESLManager{
		connected:     true,
		listeners:     make(map[string]map[string]esl.EventHandler),
		eventHandlers: make(map[string]esl.EventHandler),
	}
}

func (m *MockESLManager) Connect(ctx context.Context) error {
	m.connected = true
	return nil
}

func (m *MockESLManager) Disconnect() error {
	m.connected = false
	return nil
}

func (m *MockESLManager) IsConnected() bool {
	return m.connected
}

func (m *MockESLManager) GetConnection() (*eslgo.Conn, error) {
	if !m.connected {
		return nil, esl.ErrESLNotConnected
	}
	// Return error for mock since we can't create a real connection
	return nil, esl.ErrESLNotConnected
}

func (m *MockESLManager) HealthCheck(ctx context.Context) error {
	if !m.connected {
		return esl.ErrESLNotConnected
	}
	return nil
}

func (m *MockESLManager) GetHealthStatus() esl.ConnectionStatus {
	return esl.ConnectionStatus{Connected: m.connected}
}

func (m *MockESLManager) TriggerHealthCheck() error {
	return m.HealthCheck(context.Background())
}

func (m *MockESLManager) RegisterEventListener(eventType string, handler esl.EventHandler) (string, error) {
	args := m.Called(eventType, handler)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.listeners[eventType] == nil {
		m.listeners[eventType] = make(map[string]esl.EventHandler)
	}
	listenerID := args.String(0)
	if listenerID == "" {
		listenerID = "test-listener-1"
	}
	m.listeners[eventType][listenerID] = handler
	m.eventHandlers[listenerID] = handler

	return listenerID, args.Error(1)
}

func (m *MockESLManager) RemoveEventListener(eventType string, listenerID string) error {
	args := m.Called(eventType, listenerID)

	m.mu.Lock()
	defer m.mu.Unlock()

	if listeners, exists := m.listeners[eventType]; exists {
		delete(listeners, listenerID)
	}
	delete(m.eventHandlers, listenerID)

	return args.Error(0)
}

func (m *MockESLManager) GetEventListenerCount(eventType string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if listeners, exists := m.listeners[eventType]; exists {
		return len(listeners)
	}
	return 0
}

func (m *MockESLManager) GetRegisteredEventTypes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	eventTypes := make([]string, 0, len(m.listeners))
	for eventType := range m.listeners {
		eventTypes = append(eventTypes, eventType)
	}
	return eventTypes
}

// SimulateEvent 模擬事件觸發
func (m *MockESLManager) SimulateEvent(event *eslgo.Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, handler := range m.eventHandlers {
		go handler(event)
	}
}

func TestScanServiceWithESLManager(t *testing.T) {
	// Create mock ESL manager
	mockESLManager := NewMockESLManager()

	// Create scan service with mock ESL manager
	scanService := NewScanService(nil, nil, mockESLManager)

	// Verify that the service was created successfully
	if scanService == nil {
		t.Fatal("Expected scan service to be created, got nil")
	}

	if scanService.eslManager == nil {
		t.Fatal("Expected ESL manager to be set, got nil")
	}

	// Test that we can register event listeners
	config := &ScanConfig{
		Numbers:     []string{"1234567890"},
		RingTime:    30,
		Concurrency: 1,
		DialDelay:   100,
	}

	// This will fail because we don't have a real connection, but it should
	// at least test the ESL manager integration
	_, err := scanService.Scan(context.Background(), config)
	if err == nil {
		t.Fatal("Expected error due to mock connection, got nil")
	}

	// The error should be about getting the connection, not about missing ESL manager
	expectedError := "error getting ESL connection"
	if err.Error()[:len(expectedError)] != expectedError {
		t.Errorf("Expected error to start with '%s', got: %s", expectedError, err.Error())
	}
}

func TestScanServiceESLManagerIntegration(t *testing.T) {
	mockESLManager := NewMockESLManager()
	scanService := NewScanService(nil, nil, mockESLManager)

	// Test that ESL manager is properly integrated
	if !scanService.eslManager.IsConnected() {
		t.Error("Expected ESL manager to be connected")
	}

	// Set up mock expectations
	mockESLManager.On("RegisterEventListener", "TEST_EVENT", mock.AnythingOfType("esl.EventHandler")).Return("test-listener-1", nil)
	mockESLManager.On("RemoveEventListener", "TEST_EVENT", "test-listener-1").Return(nil)

	// Test event listener registration
	listenerID, err := scanService.eslManager.RegisterEventListener("TEST_EVENT", func(event *eslgo.Event) {
		// Test handler
	})
	if err != nil {
		t.Errorf("Expected no error registering event listener, got: %v", err)
	}

	if listenerID == "" {
		t.Error("Expected non-empty listener ID")
	}

	// Test event listener removal
	err = scanService.eslManager.RemoveEventListener("TEST_EVENT", listenerID)
	if err != nil {
		t.Errorf("Expected no error removing event listener, got: %v", err)
	}

	// Verify mock expectations
	mockESLManager.AssertExpectations(t)
}
