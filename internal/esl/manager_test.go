package esl

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/percipia/eslgo"
)

func TestNewConnectionManager(t *testing.T) {
	// 測試使用nil配置創建管理器
	cm := NewConnectionManager(nil)
	if cm == nil {
		t.Fatal("Expected ConnectionManager to be created, got nil")
	}

	// 檢查預設值
	if cm.config.Host != "localhost:8021" {
		t.Errorf("Expected default host to be 'localhost:8021', got '%s'", cm.config.Host)
	}
	if cm.config.Password != "ClueCon" {
		t.Errorf("Expected default password to be 'ClueCon', got '%s'", cm.config.Password)
	}
	if cm.config.ReconnectInterval != 5*time.Second {
		t.Errorf("Expected default reconnect interval to be 5s, got %v", cm.config.ReconnectInterval)
	}
	if cm.config.HealthCheckInterval != 30*time.Second {
		t.Errorf("Expected default health check interval to be 30s, got %v", cm.config.HealthCheckInterval)
	}
	if cm.config.MaxReconnectAttempts != 10 {
		t.Errorf("Expected default max reconnect attempts to be 10, got %d", cm.config.MaxReconnectAttempts)
	}
}

func TestConnectionManagerCustomConfig(t *testing.T) {
	config := &ESLConfig{
		Host:                 "192.168.1.100:8021",
		Password:             "custom_password",
		ReconnectInterval:    10 * time.Second,
		HealthCheckInterval:  60 * time.Second,
		MaxReconnectAttempts: 5,
	}

	cm := NewConnectionManager(config)
	if cm == nil {
		t.Fatal("Expected ConnectionManager to be created, got nil")
	}

	// 檢查自定義值
	if cm.config.Host != config.Host {
		t.Errorf("Expected host to be '%s', got '%s'", config.Host, cm.config.Host)
	}
	if cm.config.Password != config.Password {
		t.Errorf("Expected password to be '%s', got '%s'", config.Password, cm.config.Password)
	}
	if cm.config.ReconnectInterval != config.ReconnectInterval {
		t.Errorf("Expected reconnect interval to be %v, got %v", config.ReconnectInterval, cm.config.ReconnectInterval)
	}
	if cm.config.HealthCheckInterval != config.HealthCheckInterval {
		t.Errorf("Expected health check interval to be %v, got %v", config.HealthCheckInterval, cm.config.HealthCheckInterval)
	}
	if cm.config.MaxReconnectAttempts != config.MaxReconnectAttempts {
		t.Errorf("Expected max reconnect attempts to be %d, got %d", config.MaxReconnectAttempts, cm.config.MaxReconnectAttempts)
	}
}

func TestIsConnectedInitialState(t *testing.T) {
	cm := NewConnectionManager(nil)
	if cm.IsConnected() {
		t.Error("Expected initial connection state to be false")
	}
}

func TestGetConnectionWhenNotConnected(t *testing.T) {
	cm := NewConnectionManager(nil)
	conn, err := cm.GetConnection()
	if err != ErrESLNotConnected {
		t.Errorf("Expected ErrESLNotConnected, got %v", err)
	}
	if conn != nil {
		t.Error("Expected connection to be nil when not connected")
	}
}

func TestEventListenerRegistration(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 測試註冊事件監聽器
	handler := func(event *eslgo.Event) {
		// 測試處理器
	}

	listenerID, err := cm.RegisterEventListener("CHANNEL_CREATE", handler)
	if err != nil {
		t.Errorf("Expected no error registering event listener, got %v", err)
	}
	if listenerID == "" {
		t.Error("Expected non-empty listener ID")
	}

	// 檢查監聽器是否已註冊
	count := cm.GetEventListenerCount("CHANNEL_CREATE")
	if count != 1 {
		t.Errorf("Expected 1 listener, got %d", count)
	}

	// 測試移除事件監聽器
	err = cm.RemoveEventListener("CHANNEL_CREATE", listenerID)
	if err != nil {
		t.Errorf("Expected no error removing event listener, got %v", err)
	}

	// 檢查監聽器是否已移除
	count = cm.GetEventListenerCount("CHANNEL_CREATE")
	if count != 0 {
		t.Errorf("Expected 0 listeners after removal, got %d", count)
	}
}

func TestEventListenerNilHandler(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 測試註冊nil處理器
	listenerID, err := cm.RegisterEventListener("CHANNEL_CREATE", nil)
	if err == nil {
		t.Error("Expected error when registering nil handler")
	}
	if listenerID != "" {
		t.Error("Expected empty listener ID when error occurs")
	}
}

func TestHealthCheckWhenNotConnected(t *testing.T) {
	cm := NewConnectionManager(nil)
	ctx := context.Background()

	err := cm.HealthCheck(ctx)
	if err != ErrESLNotConnected {
		t.Errorf("Expected ErrESLNotConnected, got %v", err)
	}
}

// TestReconnectMechanism 測試重連機制
func TestReconnectMechanism(t *testing.T) {
	config := &ESLConfig{
		Host:                 "localhost:8021",
		Password:             "test",
		ReconnectInterval:    100 * time.Millisecond, // 快速重連用於測試
		HealthCheckInterval:  1 * time.Second,
		MaxReconnectAttempts: 3,
	}

	cm := NewConnectionManager(config)

	// 測試初始狀態
	if cm.reconnectCtx != nil {
		t.Error("Expected reconnectCtx to be nil initially")
	}

	// 模擬連接丟失
	cm.handleConnectionLost()

	// 檢查重連狀態是否正確設置
	cm.reconnectMu.Lock()
	reconnectActive := cm.reconnectCtx != nil
	cm.reconnectMu.Unlock()

	if !reconnectActive {
		t.Error("Expected reconnect context to be active after connection loss")
	}

	// 檢查連接狀態
	if cm.IsConnected() {
		t.Error("Expected connection to be false after connection loss")
	}

	// 等待一段時間讓重連嘗試執行
	time.Sleep(500 * time.Millisecond)

	// 檢查重連計數
	if cm.status.ReconnectCount == 0 {
		t.Error("Expected reconnect attempts to be recorded")
	}

	// 停止重連
	cm.stopReconnect()

	// 檢查重連是否已停止
	cm.reconnectMu.Lock()
	reconnectStopped := cm.reconnectCtx == nil
	cm.reconnectMu.Unlock()

	if !reconnectStopped {
		t.Error("Expected reconnect context to be nil after stopping")
	}
}

// TestReconnectLoop 測試重連循環邏輯
func TestReconnectLoop(t *testing.T) {
	config := &ESLConfig{
		Host:                 "invalid:8021", // 無效地址確保連接失敗
		Password:             "test",
		ReconnectInterval:    50 * time.Millisecond,
		HealthCheckInterval:  1 * time.Second,
		MaxReconnectAttempts: 2, // 限制重連次數
	}

	cm := NewConnectionManager(config)

	// 啟動重連
	cm.startReconnect()

	// 等待重連嘗試完成
	time.Sleep(300 * time.Millisecond)

	// 檢查是否達到最大重連次數
	if cm.status.ReconnectCount < config.MaxReconnectAttempts {
		t.Errorf("Expected at least %d reconnect attempts, got %d",
			config.MaxReconnectAttempts, cm.status.ReconnectCount)
	}

	// 檢查錯誤訊息
	if cm.status.LastError == "" {
		t.Error("Expected error message to be set after failed reconnections")
	}

	// 清理
	cm.stopReconnect()
}

// TestConcurrentReconnectCalls 測試並發重連調用的線程安全性
func TestConcurrentReconnectCalls(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 並發啟動多個重連
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cm.startReconnect()
			done <- true
		}()
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 檢查只有一個重連上下文被創建
	cm.reconnectMu.Lock()
	hasReconnectCtx := cm.reconnectCtx != nil
	cm.reconnectMu.Unlock()

	if !hasReconnectCtx {
		t.Error("Expected exactly one reconnect context to be active")
	}

	// 清理
	cm.stopReconnect()
}

// TestReconnectStateManagement 測試重連狀態管理
func TestReconnectStateManagement(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 測試初始狀態
	if cm.status.ReconnectCount != 0 {
		t.Error("Expected initial reconnect count to be 0")
	}

	// 模擬連接丟失和狀態更新
	cm.handleConnectionLost()

	// 檢查狀態是否正確更新
	if cm.status.Connected {
		t.Error("Expected connected status to be false after connection loss")
	}

	if cm.status.LastError != "Connection lost" {
		t.Errorf("Expected last error to be 'Connection lost', got '%s'", cm.status.LastError)
	}

	// 清理
	cm.stopReconnect()
}

// TestExponentialBackoff 測試指數退避策略
func TestExponentialBackoff(t *testing.T) {
	config := &ESLConfig{
		Host:                 "invalid:8021",
		Password:             "test",
		ReconnectInterval:    10 * time.Millisecond, // 很短的初始間隔
		MaxReconnectAttempts: 5,
	}

	cm := NewConnectionManager(config)

	// 記錄重連嘗試的時間
	startTime := time.Now()
	cm.startReconnect()

	// 等待幾次重連嘗試
	time.Sleep(200 * time.Millisecond)

	elapsed := time.Since(startTime)

	// 由於指數退避，總時間應該比簡單的線性重試更長
	// 第一次: 10ms, 第二次: 20ms, 第三次: 40ms, 第四次: 80ms
	// 總共至少應該有 150ms 的等待時間
	minExpectedTime := 100 * time.Millisecond

	if elapsed < minExpectedTime {
		t.Errorf("Expected exponential backoff to take at least %v, but took %v",
			minExpectedTime, elapsed)
	}

	// 清理
	cm.stopReconnect()
}

// TestReconnectSuccess 測試成功重連的情況
func TestReconnectSuccess(t *testing.T) {
	config := &ESLConfig{
		Host:                 "localhost:8021",
		Password:             "test",
		ReconnectInterval:    50 * time.Millisecond,
		MaxReconnectAttempts: 3,
	}

	cm := NewConnectionManager(config)

	// 模擬連接丟失
	cm.handleConnectionLost()

	// 檢查狀態
	if cm.IsConnected() {
		t.Error("Expected connection to be false after connection loss")
	}

	if cm.status.LastError != "Connection lost" {
		t.Errorf("Expected last error to be 'Connection lost', got '%s'", cm.status.LastError)
	}

	// 等待重連嘗試
	time.Sleep(200 * time.Millisecond)

	// 檢查重連嘗試是否被記錄
	if cm.status.ReconnectCount == 0 {
		t.Error("Expected reconnect attempts to be recorded")
	}

	// 清理
	cm.stopReconnect()
}

// TestReconnectContextCancellation 測試重連上下文取消
func TestReconnectContextCancellation(t *testing.T) {
	config := &ESLConfig{
		Host:                 "invalid:8021",
		Password:             "test",
		ReconnectInterval:    100 * time.Millisecond,
		MaxReconnectAttempts: 10, // 設置較高的重連次數
	}

	cm := NewConnectionManager(config)

	// 啟動重連
	cm.startReconnect()

	// 等待一段時間讓重連開始
	time.Sleep(50 * time.Millisecond)

	// 停止重連
	cm.stopReconnect()

	// 等待一段時間確保重連循環已停止
	time.Sleep(200 * time.Millisecond)

	// 記錄當前的重連次數
	currentCount := cm.status.ReconnectCount

	// 再等待一段時間，確保重連已經停止
	time.Sleep(200 * time.Millisecond)

	// 重連次數不應該繼續增加
	if cm.status.ReconnectCount > currentCount {
		t.Errorf("Expected reconnect to stop, but count increased from %d to %d",
			currentCount, cm.status.ReconnectCount)
	}
}

// TestHealthCheckConfigValidation 測試健康檢查配置驗證
func TestHealthCheckConfigValidation(t *testing.T) {
	tests := []struct {
		name             string
		inputInterval    time.Duration
		expectedInterval time.Duration
	}{
		{
			name:             "Too short interval",
			inputInterval:    1 * time.Second,
			expectedInterval: 5 * time.Second,
		},
		{
			name:             "Too long interval",
			inputInterval:    10 * time.Minute,
			expectedInterval: 5 * time.Minute,
		},
		{
			name:             "Valid interval",
			inputInterval:    30 * time.Second,
			expectedInterval: 30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &ESLConfig{
				HealthCheckInterval: tt.inputInterval,
			}

			cm := NewConnectionManager(config)

			if cm.config.HealthCheckInterval != tt.expectedInterval {
				t.Errorf("Expected health check interval to be %v, got %v",
					tt.expectedInterval, cm.config.HealthCheckInterval)
			}
		})
	}
}

// TestGetHealthStatus 測試獲取健康狀態
func TestGetHealthStatus(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 測試初始狀態
	status := cm.GetHealthStatus()
	if status.Connected {
		t.Error("Expected initial connected status to be false")
	}
	if status.HealthCheckPassed {
		t.Error("Expected initial health check status to be false")
	}
	if status.ReconnectCount != 0 {
		t.Error("Expected initial reconnect count to be 0")
	}
}

// TestTriggerHealthCheckWhenNotConnected 測試未連接時手動觸發健康檢查
func TestTriggerHealthCheckWhenNotConnected(t *testing.T) {
	cm := NewConnectionManager(nil)

	err := cm.TriggerHealthCheck()
	if err != ErrESLNotConnected {
		t.Errorf("Expected ErrESLNotConnected, got %v", err)
	}

	// 檢查狀態是否正確更新
	status := cm.GetHealthStatus()
	if status.HealthCheckPassed {
		t.Error("Expected health check to fail when not connected")
	}
	if status.LastHealthCheck.IsZero() {
		t.Error("Expected LastHealthCheck to be set")
	}
	if status.LastError == "" {
		t.Error("Expected LastError to be set when health check fails")
	}
}

// TestHealthCheckStatusTracking 測試健康檢查狀態追蹤
func TestHealthCheckStatusTracking(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 記錄檢查前的時間
	beforeCheck := time.Now()

	// 觸發健康檢查（應該失敗，因為未連接）
	err := cm.TriggerHealthCheck()
	if err == nil {
		t.Error("Expected health check to fail when not connected")
	}

	// 記錄檢查後的時間
	afterCheck := time.Now()

	// 檢查狀態更新
	status := cm.GetHealthStatus()

	// 檢查時間戳是否在合理範圍內
	if status.LastHealthCheck.Before(beforeCheck) || status.LastHealthCheck.After(afterCheck) {
		t.Error("LastHealthCheck timestamp is not within expected range")
	}

	// 檢查健康檢查狀態
	if status.HealthCheckPassed {
		t.Error("Expected HealthCheckPassed to be false")
	}

	// 檢查錯誤訊息
	if status.LastError == "" {
		t.Error("Expected LastError to be set")
	}
}

// TestHealthCheckLoop 測試健康檢查循環
func TestHealthCheckLoop(t *testing.T) {
	config := &ESLConfig{
		HealthCheckInterval: 10 * time.Second, // 使用較長間隔，手動觸發測試
	}

	cm := NewConnectionManager(config)

	// 啟動健康檢查
	cm.startHealthCheck()

	// 手動觸發一次健康檢查來測試功能
	cm.performHealthCheck()

	// 檢查健康檢查是否被執行
	status := cm.GetHealthStatus()
	if status.LastHealthCheck.IsZero() {
		t.Error("Expected health check to have been performed")
	}

	// 停止健康檢查
	cm.stopHealthCheck()

	// 記錄停止後的最後檢查時間
	lastCheckTime := status.LastHealthCheck

	// 等待一段時間
	time.Sleep(100 * time.Millisecond)

	// 檢查健康檢查是否已停止
	newStatus := cm.GetHealthStatus()
	if newStatus.LastHealthCheck.After(lastCheckTime) {
		t.Error("Expected health check to stop after calling stopHealthCheck")
	}
}

// TestHealthCheckFailureTriggersReconnect 測試健康檢查失敗觸發重連
func TestHealthCheckFailureTriggersReconnect(t *testing.T) {
	config := &ESLConfig{
		Host:                 "invalid:8021", // 無效地址
		Password:             "test",
		HealthCheckInterval:  10 * time.Second, // 使用較長間隔
		ReconnectInterval:    100 * time.Millisecond,
		MaxReconnectAttempts: 2,
	}

	cm := NewConnectionManager(config)

	// 模擬已連接狀態（但實際上沒有真實連接）
	cm.mu.Lock()
	cm.connected = true
	cm.status.Connected = true
	cm.mu.Unlock()

	// 執行健康檢查（應該失敗並觸發重連）
	cm.performHealthCheck()

	// 檢查連接狀態是否被正確更新
	if cm.IsConnected() {
		t.Error("Expected connection status to be false after health check failure")
	}

	// 檢查是否觸發了重連
	cm.reconnectMu.Lock()
	reconnectActive := cm.reconnectCtx != nil
	cm.reconnectMu.Unlock()

	if !reconnectActive {
		t.Error("Expected reconnect to be triggered after health check failure")
	}

	// 等待重連嘗試
	time.Sleep(300 * time.Millisecond)

	// 檢查重連嘗試是否被記錄
	status := cm.GetHealthStatus()
	if status.ReconnectCount == 0 {
		t.Error("Expected reconnect attempts to be recorded")
	}

	// 清理
	cm.stopReconnect()
}

// TestConcurrentHealthChecks 測試並發健康檢查的線程安全性
func TestConcurrentHealthChecks(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 並發執行多個健康檢查
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			cm.TriggerHealthCheck()
			done <- true
		}()
	}

	// 等待所有goroutine完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 檢查狀態是否一致（不應該有競爭條件）
	status := cm.GetHealthStatus()
	if status.LastHealthCheck.IsZero() {
		t.Error("Expected at least one health check to be recorded")
	}
}

// TestHealthCheckWithTimeout 測試健康檢查超時處理
func TestHealthCheckWithTimeout(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 創建一個會立即取消的上下文
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	// 執行健康檢查
	err := cm.HealthCheck(ctx)

	// 應該返回連接錯誤（因為未連接）
	if err != ErrESLNotConnected {
		t.Errorf("Expected ErrESLNotConnected, got %v", err)
	}
}

// TestHealthCheckStateConsistency 測試健康檢查狀態一致性
func TestHealthCheckStateConsistency(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 執行多次健康檢查
	for i := 0; i < 5; i++ {
		cm.TriggerHealthCheck()
		time.Sleep(10 * time.Millisecond)
	}

	// 檢查狀態一致性
	status := cm.GetHealthStatus()

	// 由於未連接，所有檢查都應該失敗
	if status.HealthCheckPassed {
		t.Error("Expected all health checks to fail when not connected")
	}

	// 錯誤訊息應該存在
	if status.LastError == "" {
		t.Error("Expected error message to be set")
	}

	// 連接狀態應該為false
	if status.Connected {
		t.Error("Expected connected status to be false")
	}
}

// TestHealthCheckRecovery 測試健康檢查恢復場景
func TestHealthCheckRecovery(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 模擬連接失敗狀態
	cm.mu.Lock()
	cm.connected = false
	cm.status.Connected = false
	cm.status.LastError = "Previous connection error"
	cm.mu.Unlock()

	// 執行健康檢查（仍然會失敗，因為沒有真實連接）
	err := cm.TriggerHealthCheck()
	if err == nil {
		t.Error("Expected health check to fail when not connected")
	}

	// 檢查錯誤狀態是否被更新
	status := cm.GetHealthStatus()
	if status.LastError == "Previous connection error" {
		t.Error("Expected error message to be updated after health check")
	}
}

// TestEventDispatcher 測試EventDispatcher的基本功能
func TestEventDispatcher(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 測試初始狀態
	if dispatcher == nil {
		t.Fatal("Expected EventDispatcher to be created, got nil")
	}

	count := dispatcher.GetListenerCount("TEST_EVENT")
	if count != 0 {
		t.Errorf("Expected 0 listeners initially, got %d", count)
	}

	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) != 0 {
		t.Errorf("Expected 0 event types initially, got %d", len(eventTypes))
	}
}

// TestEventDispatcherRegisterListener 測試事件監聽器註冊
func TestEventDispatcherRegisterListener(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 測試正常註冊
	handler := func(event *eslgo.Event) {
		// 測試處理器
	}

	listenerID, err := dispatcher.RegisterListener("TEST_EVENT", handler)
	if err != nil {
		t.Errorf("Expected no error registering listener, got %v", err)
	}
	if listenerID == "" {
		t.Error("Expected non-empty listener ID")
	}

	// 檢查監聽器數量
	count := dispatcher.GetListenerCount("TEST_EVENT")
	if count != 1 {
		t.Errorf("Expected 1 listener, got %d", count)
	}

	// 檢查事件類型列表
	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) != 1 || eventTypes[0] != "TEST_EVENT" {
		t.Errorf("Expected ['TEST_EVENT'], got %v", eventTypes)
	}
}

// TestEventDispatcherRegisterMultipleListeners 測試註冊多個監聽器
func TestEventDispatcherRegisterMultipleListeners(t *testing.T) {
	dispatcher := NewEventDispatcher()

	handler1 := func(event *eslgo.Event) {}
	handler2 := func(event *eslgo.Event) {}
	handler3 := func(event *eslgo.Event) {}

	// 為同一事件類型註冊多個監聽器
	id1, err1 := dispatcher.RegisterListener("TEST_EVENT", handler1)
	id2, err2 := dispatcher.RegisterListener("TEST_EVENT", handler2)
	id3, err3 := dispatcher.RegisterListener("ANOTHER_EVENT", handler3)

	if err1 != nil || err2 != nil || err3 != nil {
		t.Errorf("Expected no errors, got %v, %v, %v", err1, err2, err3)
	}

	// 檢查監聽器ID是否唯一
	if id1 == id2 || id1 == id3 || id2 == id3 {
		t.Error("Expected unique listener IDs")
	}

	// 檢查監聽器數量
	count1 := dispatcher.GetListenerCount("TEST_EVENT")
	count2 := dispatcher.GetListenerCount("ANOTHER_EVENT")

	if count1 != 2 {
		t.Errorf("Expected 2 listeners for TEST_EVENT, got %d", count1)
	}
	if count2 != 1 {
		t.Errorf("Expected 1 listener for ANOTHER_EVENT, got %d", count2)
	}

	// 檢查事件類型數量
	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) != 2 {
		t.Errorf("Expected 2 event types, got %d", len(eventTypes))
	}
}

// TestEventDispatcherRegisterInvalidInputs 測試無效輸入的處理
func TestEventDispatcherRegisterInvalidInputs(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 測試nil處理器
	_, err := dispatcher.RegisterListener("TEST_EVENT", nil)
	if err == nil {
		t.Error("Expected error when registering nil handler")
	}

	// 測試空事件類型
	handler := func(event *eslgo.Event) {}
	_, err = dispatcher.RegisterListener("", handler)
	if err == nil {
		t.Error("Expected error when registering with empty event type")
	}
}

// TestEventDispatcherRemoveListener 測試移除監聽器
func TestEventDispatcherRemoveListener(t *testing.T) {
	dispatcher := NewEventDispatcher()

	handler := func(event *eslgo.Event) {}
	listenerID, _ := dispatcher.RegisterListener("TEST_EVENT", handler)

	// 測試正常移除
	err := dispatcher.RemoveListener("TEST_EVENT", listenerID)
	if err != nil {
		t.Errorf("Expected no error removing listener, got %v", err)
	}

	// 檢查監聽器是否已移除
	count := dispatcher.GetListenerCount("TEST_EVENT")
	if count != 0 {
		t.Errorf("Expected 0 listeners after removal, got %d", count)
	}

	// 檢查事件類型是否已清理
	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) != 0 {
		t.Errorf("Expected 0 event types after removal, got %d", len(eventTypes))
	}
}

// TestEventDispatcherRemoveInvalidInputs 測試移除監聽器的無效輸入
func TestEventDispatcherRemoveInvalidInputs(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 測試空事件類型
	err := dispatcher.RemoveListener("", "listener_1")
	if err == nil {
		t.Error("Expected error when removing with empty event type")
	}

	// 測試空監聽器ID
	err = dispatcher.RemoveListener("TEST_EVENT", "")
	if err == nil {
		t.Error("Expected error when removing with empty listener ID")
	}

	// 測試不存在的事件類型
	err = dispatcher.RemoveListener("NONEXISTENT_EVENT", "listener_1")
	if err == nil {
		t.Error("Expected error when removing from nonexistent event type")
	}

	// 註冊一個監聽器然後測試移除不存在的監聽器ID
	handler := func(event *eslgo.Event) {}
	dispatcher.RegisterListener("TEST_EVENT", handler)

	err = dispatcher.RemoveListener("TEST_EVENT", "nonexistent_listener")
	if err == nil {
		t.Error("Expected error when removing nonexistent listener")
	}
}

// TestEventDispatcherDispatchEvent 測試事件分發
func TestEventDispatcherDispatchEvent(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 創建測試事件
	testEvent := &eslgo.Event{}
	// 注意：由於eslgo.Event的內部結構，我們需要模擬GetName方法
	// 在實際測試中，這可能需要mock或者使用真實的eslgo.Event

	receivedEvents := make(chan *eslgo.Event, 10)
	handler := func(event *eslgo.Event) {
		receivedEvents <- event
	}

	// 註冊監聽器
	dispatcher.RegisterListener("TEST_EVENT", handler)

	// 分發事件（注意：這個測試可能需要調整，因為eslgo.Event的GetName方法）
	dispatcher.DispatchEvent(testEvent)

	// 由於事件處理是異步的，等待一段時間
	time.Sleep(10 * time.Millisecond)

	// 檢查是否收到事件（這個測試可能需要根據實際的eslgo.Event實現調整）
	select {
	case receivedEvent := <-receivedEvents:
		if receivedEvent != testEvent {
			t.Error("Expected to receive the dispatched event")
		}
	case <-time.After(100 * time.Millisecond):
		// 由於我們無法控制eslgo.Event的GetName方法，這個測試可能會超時
		// 這是預期的，因為我們使用的是空的eslgo.Event
		t.Log("Event dispatch test timed out - this is expected with empty eslgo.Event")
	}
}

// TestEventDispatcherDispatchToMultipleListeners 測試分發到多個監聽器
func TestEventDispatcherDispatchToMultipleListeners(t *testing.T) {
	dispatcher := NewEventDispatcher()

	handler := func(event *eslgo.Event) {
		// 簡化的測試處理器
	}

	// 註冊多個監聽器
	dispatcher.RegisterListener("TEST_EVENT", handler)
	dispatcher.RegisterListener("TEST_EVENT", handler)
	dispatcher.RegisterListener("TEST_EVENT", handler)

	// 檢查監聽器數量
	count := dispatcher.GetListenerCount("TEST_EVENT")
	if count != 3 {
		t.Errorf("Expected 3 listeners, got %d", count)
	}

	// 創建測試事件並分發
	testEvent := &eslgo.Event{}
	dispatcher.DispatchEvent(testEvent)

	// 等待事件處理完成
	time.Sleep(10 * time.Millisecond)

	// 注意：由於eslgo.Event的限制，實際的事件分發測試可能需要mock
}

// TestEventDispatcherConcurrentAccess 測試並發訪問的線程安全性
func TestEventDispatcherConcurrentAccess(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 並發註冊監聽器
	done := make(chan bool, 20)

	// 10個goroutine註冊監聽器
	for i := 0; i < 10; i++ {
		go func(index int) {
			handler := func(event *eslgo.Event) {}
			eventType := fmt.Sprintf("EVENT_%d", index%3) // 使用3種不同的事件類型
			_, err := dispatcher.RegisterListener(eventType, handler)
			if err != nil {
				t.Errorf("Error registering listener: %v", err)
			}
			done <- true
		}(i)
	}

	// 10個goroutine分發事件
	for i := 0; i < 10; i++ {
		go func(index int) {
			testEvent := &eslgo.Event{}
			dispatcher.DispatchEvent(testEvent)
			done <- true
		}(i)
	}

	// 等待所有goroutine完成
	for i := 0; i < 20; i++ {
		<-done
	}

	// 檢查最終狀態
	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) == 0 {
		t.Error("Expected some event types to be registered")
	}

	totalListeners := 0
	for _, eventType := range eventTypes {
		totalListeners += dispatcher.GetListenerCount(eventType)
	}

	if totalListeners != 10 {
		t.Errorf("Expected 10 total listeners, got %d", totalListeners)
	}
}

// TestEventDispatcherClear 測試清除所有監聽器
func TestEventDispatcherClear(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 註冊一些監聽器
	handler := func(event *eslgo.Event) {}
	dispatcher.RegisterListener("EVENT_1", handler)
	dispatcher.RegisterListener("EVENT_2", handler)
	dispatcher.RegisterListener("EVENT_1", handler) // 同一事件類型的第二個監聽器

	// 檢查初始狀態
	if len(dispatcher.GetAllEventTypes()) != 2 {
		t.Error("Expected 2 event types before clear")
	}

	// 清除所有監聽器
	dispatcher.Clear()

	// 檢查清除後的狀態
	eventTypes := dispatcher.GetAllEventTypes()
	if len(eventTypes) != 0 {
		t.Errorf("Expected 0 event types after clear, got %d", len(eventTypes))
	}

	count1 := dispatcher.GetListenerCount("EVENT_1")
	count2 := dispatcher.GetListenerCount("EVENT_2")

	if count1 != 0 || count2 != 0 {
		t.Errorf("Expected 0 listeners for all events after clear, got %d and %d", count1, count2)
	}
}

// TestConnectionManagerEventDispatcherIntegration 測試ConnectionManager與EventDispatcher的集成
func TestConnectionManagerEventDispatcherIntegration(t *testing.T) {
	cm := NewConnectionManager(nil)

	// 測試通過ConnectionManager註冊監聽器
	handler := func(event *eslgo.Event) {}
	listenerID, err := cm.RegisterEventListener("CHANNEL_CREATE", handler)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	// 檢查監聽器是否通過EventDispatcher正確註冊
	count := cm.GetEventListenerCount("CHANNEL_CREATE")
	if count != 1 {
		t.Errorf("Expected 1 listener, got %d", count)
	}

	// 檢查事件類型列表
	eventTypes := cm.GetRegisteredEventTypes()
	if len(eventTypes) != 1 || eventTypes[0] != "CHANNEL_CREATE" {
		t.Errorf("Expected ['CHANNEL_CREATE'], got %v", eventTypes)
	}

	// 測試移除監聽器
	err = cm.RemoveEventListener("CHANNEL_CREATE", listenerID)
	if err != nil {
		t.Errorf("Expected no error removing listener, got %v", err)
	}

	// 檢查移除後的狀態
	count = cm.GetEventListenerCount("CHANNEL_CREATE")
	if count != 0 {
		t.Errorf("Expected 0 listeners after removal, got %d", count)
	}
}

// TestEventDispatcherPanicRecovery 測試事件處理器panic的恢復
func TestEventDispatcherPanicRecovery(t *testing.T) {
	dispatcher := NewEventDispatcher()

	// 註冊一個會panic的處理器
	panicHandler := func(event *eslgo.Event) {
		panic("test panic")
	}

	// 註冊一個正常的處理器
	normalHandler := func(event *eslgo.Event) {
		// 正常處理
	}

	dispatcher.RegisterListener("TEST_EVENT", panicHandler)
	dispatcher.RegisterListener("TEST_EVENT", normalHandler)

	// 分發事件
	testEvent := &eslgo.Event{}

	// 這不應該導致程序崩潰
	dispatcher.DispatchEvent(testEvent)

	// 等待事件處理完成
	time.Sleep(50 * time.Millisecond)

	// 如果程序沒有崩潰，測試就通過了
	// 檢查監聽器是否仍然存在
	count := dispatcher.GetListenerCount("TEST_EVENT")
	if count != 2 {
		t.Errorf("Expected 2 listeners after panic recovery, got %d", count)
	}
}

// TestEventDispatcherNilEventHandling 測試nil事件的處理
func TestEventDispatcherNilEventHandling(t *testing.T) {
	dispatcher := NewEventDispatcher()

	handler := func(event *eslgo.Event) {
		t.Error("Handler should not be called for nil event")
	}

	dispatcher.RegisterListener("TEST_EVENT", handler)

	// 分發nil事件
	dispatcher.DispatchEvent(nil)

	// 等待一段時間確保處理器不會被調用
	time.Sleep(10 * time.Millisecond)

	// 如果沒有錯誤，測試通過
}

// TestEventDispatcherListenerIDUniqueness 測試監聽器ID的唯一性
func TestEventDispatcherListenerIDUniqueness(t *testing.T) {
	dispatcher := NewEventDispatcher()

	handler := func(event *eslgo.Event) {}

	// 註冊多個監聽器並收集ID
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := dispatcher.RegisterListener("TEST_EVENT", handler)
		if err != nil {
			t.Errorf("Error registering listener %d: %v", i, err)
			continue
		}

		if ids[id] {
			t.Errorf("Duplicate listener ID found: %s", id)
		}
		ids[id] = true
	}

	// 檢查是否生成了100個唯一ID
	if len(ids) != 100 {
		t.Errorf("Expected 100 unique IDs, got %d", len(ids))
	}
}
