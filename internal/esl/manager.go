package esl

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"time"

	"numscan/internal/logger"

	"github.com/percipia/eslgo"
)

// 錯誤類型定義
var (
	ErrESLNotConnected        = errors.New("ESL connection not established")
	ErrESLConnectionLost      = errors.New("ESL connection lost")
	ErrESLReconnectFailed     = errors.New("ESL reconnection failed")
	ErrESLConfigInvalid       = errors.New("ESL configuration invalid")
	ErrESLHealthCheckFailed   = errors.New("ESL health check failed")
	ErrESLEventDispatchFailed = errors.New("ESL event dispatch failed")
	ErrESLListenerNotFound    = errors.New("ESL event listener not found")
	ErrESLInvalidEventType    = errors.New("ESL invalid event type")
	ErrESLConnectionTimeout   = errors.New("ESL connection timeout")
)

// ESLError 包裝ESL相關錯誤，提供更多上下文信息
type ESLError struct {
	Op      string                 // 操作名稱
	Host    string                 // ESL主機
	Err     error                  // 原始錯誤
	Code    ErrorCode              // 錯誤代碼
	Context map[string]interface{} // 額外上下文
}

// ErrorCode 定義錯誤代碼
type ErrorCode int

const (
	ErrorCodeConnection ErrorCode = iota + 1000
	ErrorCodeReconnection
	ErrorCodeHealthCheck
	ErrorCodeEventDispatch
	ErrorCodeConfiguration
	ErrorCodeTimeout
	ErrorCodeInvalidInput
)

// Error 實現error接口
func (e *ESLError) Error() string {
	if e.Context != nil && len(e.Context) > 0 {
		return fmt.Sprintf("ESL %s failed on %s: %v (code: %d, context: %+v)",
			e.Op, e.Host, e.Err, e.Code, e.Context)
	}
	return fmt.Sprintf("ESL %s failed on %s: %v (code: %d)",
		e.Op, e.Host, e.Err, e.Code)
}

// Unwrap 支持errors.Unwrap
func (e *ESLError) Unwrap() error {
	return e.Err
}

// NewESLError 創建新的ESL錯誤
func NewESLError(op, host string, err error, code ErrorCode, context map[string]interface{}) *ESLError {
	return &ESLError{
		Op:      op,
		Host:    host,
		Err:     err,
		Code:    code,
		Context: context,
	}
}

// RecoveryStrategy 定義錯誤恢復策略
type RecoveryStrategy int

const (
	RecoveryStrategyNone RecoveryStrategy = iota
	RecoveryStrategyReconnect
	RecoveryStrategyRestart
	RecoveryStrategyCircuitBreaker
)

// ErrorRecoveryConfig 錯誤恢復配置
type ErrorRecoveryConfig struct {
	MaxRetries              int           `json:"max_retries"`
	RetryInterval           time.Duration `json:"retry_interval"`
	CircuitBreakerThreshold int           `json:"circuit_breaker_threshold"`
	CircuitBreakerTimeout   time.Duration `json:"circuit_breaker_timeout"`
	EnableAutoRecovery      bool          `json:"enable_auto_recovery"`
}

// EventHandler 定義事件處理函數類型
type EventHandler func(event *eslgo.Event)

// EventDispatcher 管理多個事件監聽器的分發機制
type EventDispatcher struct {
	listeners     map[string]map[string]EventHandler // eventType -> listenerID -> handler
	mu            sync.RWMutex
	listenerIDGen int
}

// NewEventDispatcher 創建新的事件分發器
func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		listeners: make(map[string]map[string]EventHandler),
	}
}

// RegisterListener 註冊事件監聽器
func (ed *EventDispatcher) RegisterListener(eventType string, handler EventHandler) (string, error) {
	if handler == nil {
		return "", NewESLError("register_listener", "",
			errors.New("event handler cannot be nil"),
			ErrorCodeInvalidInput, nil)
	}
	if eventType == "" {
		return "", NewESLError("register_listener", "",
			errors.New("event type cannot be empty"),
			ErrorCodeInvalidInput, nil)
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	// 生成唯一的監聽器ID
	ed.listenerIDGen++
	listenerID := fmt.Sprintf("listener_%d_%d", time.Now().UnixNano(), ed.listenerIDGen)

	// 初始化事件類型的監聽器映射
	if ed.listeners[eventType] == nil {
		ed.listeners[eventType] = make(map[string]EventHandler)
	}

	ed.listeners[eventType][listenerID] = handler

	// 計算總監聽器數量
	totalListeners := ed.getTotalListenersUnsafe()

	// 記錄監聽器註冊
	logger.GetDefaultLogger().LogEventListenerRegistered(eventType, listenerID, totalListeners)

	return listenerID, nil
}

// getTotalListenersUnsafe 獲取總監聽器數量（不加鎖版本）
func (ed *EventDispatcher) getTotalListenersUnsafe() int {
	total := 0
	for _, listeners := range ed.listeners {
		total += len(listeners)
	}
	return total
}

// RemoveListener 移除事件監聽器
func (ed *EventDispatcher) RemoveListener(eventType string, listenerID string) error {
	if eventType == "" {
		return NewESLError("remove_listener", "",
			errors.New("event type cannot be empty"),
			ErrorCodeInvalidInput, nil)
	}
	if listenerID == "" {
		return NewESLError("remove_listener", "",
			errors.New("listener ID cannot be empty"),
			ErrorCodeInvalidInput, nil)
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()

	if listeners, exists := ed.listeners[eventType]; exists {
		if _, found := listeners[listenerID]; found {
			delete(listeners, listenerID)

			// 計算剩餘監聽器數量
			totalListeners := ed.getTotalListenersUnsafe()

			// 記錄監聽器移除
			logger.GetDefaultLogger().LogEventListenerRemoved(eventType, listenerID, totalListeners)

			// 如果該事件類型沒有監聽器了，清理映射
			if len(listeners) == 0 {
				delete(ed.listeners, eventType)
				logger.GetDefaultLogger().LogESLEvent(logger.ESLEventListenerRemoved, "Cleaned up empty listener map",
					slog.String("event_type", eventType),
				)
			}
			return nil
		}
		return NewESLError("remove_listener", "",
			fmt.Errorf("listener %s not found for event type %s", listenerID, eventType),
			ErrorCodeInvalidInput, map[string]interface{}{
				"event_type":  eventType,
				"listener_id": listenerID,
			})
	}

	return NewESLError("remove_listener", "",
		fmt.Errorf("no listeners registered for event type %s", eventType),
		ErrorCodeInvalidInput, map[string]interface{}{
			"event_type": eventType,
		})
}

// DispatchEvent 分發事件到所有註冊的監聽器
func (ed *EventDispatcher) DispatchEvent(event *eslgo.Event) {
	if event == nil {
		logger.GetDefaultLogger().LogEventDispatch("", 0, false, errors.New("event is nil"))
		return
	}

	eventName := event.GetName()
	if eventName == "" {
		logger.GetDefaultLogger().LogEventDispatch("", 0, false, errors.New("event name is empty"))
		return
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	dispatched := 0
	var dispatchErrors []error

	// 分發到特定事件類型的監聽器
	if listeners, exists := ed.listeners[eventName]; exists {
		for listenerID, handler := range listeners {
			dispatched++
			// 異步處理事件，避免阻塞其他監聽器
			go func(id string, h EventHandler, e *eslgo.Event, eventType string) {
				defer func() {
					if r := recover(); r != nil {
						logger.GetDefaultLogger().LogESLEvent(logger.ESLEventDispatchFailed, "Event handler panicked",
							slog.String("event_type", eventType),
							slog.String("listener_id", id),
							slog.String("panic", fmt.Sprintf("%v", r)),
						)
					}
				}()

				// 執行事件處理器
				func() {
					defer func() {
						if r := recover(); r != nil {
							// 已在上面的defer中處理
						}
					}()
					h(e)
				}()
			}(listenerID, handler, event, eventName)
		}
	}

	// 分發到所有事件監聽器 (eslgo.EventListenAll)
	if listeners, exists := ed.listeners[eslgo.EventListenAll]; exists {
		for listenerID, handler := range listeners {
			dispatched++
			// 異步處理事件，避免阻塞其他監聽器
			go func(id string, h EventHandler, e *eslgo.Event) {
				defer func() {
					if r := recover(); r != nil {
						logger.GetDefaultLogger().LogESLEvent(logger.ESLEventDispatchFailed, "Global event handler panicked",
							slog.String("listener_id", id),
							slog.String("panic", fmt.Sprintf("%v", r)),
						)
					}
				}()

				// 執行事件處理器
				func() {
					defer func() {
						if r := recover(); r != nil {
							// 已在上面的defer中處理
						}
					}()
					h(e)
				}()
			}(listenerID, handler, event)
		}
	}

	// 記錄事件分發結果
	if len(dispatchErrors) > 0 {
		logger.GetDefaultLogger().LogEventDispatch(eventName, dispatched, false,
			fmt.Errorf("dispatch errors: %v", dispatchErrors))
	} else {
		logger.GetDefaultLogger().LogEventDispatch(eventName, dispatched, true, nil)
	}
}

// GetListenerCount 獲取指定事件類型的監聽器數量
func (ed *EventDispatcher) GetListenerCount(eventType string) int {
	ed.mu.RLock()
	defer ed.mu.RUnlock()

	if listeners, exists := ed.listeners[eventType]; exists {
		return len(listeners)
	}
	return 0
}

// GetAllEventTypes 獲取所有已註冊的事件類型
func (ed *EventDispatcher) GetAllEventTypes() []string {
	ed.mu.RLock()
	defer ed.mu.RUnlock()

	eventTypes := make([]string, 0, len(ed.listeners))
	for eventType := range ed.listeners {
		eventTypes = append(eventTypes, eventType)
	}
	return eventTypes
}

// Clear 清除所有監聽器
func (ed *EventDispatcher) Clear() {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	ed.listeners = make(map[string]map[string]EventHandler)
	log.Printf("Cleared all event listeners")
}

// ESLManager 定義ESL連接管理器的接口
type ESLManager interface {
	// 連接管理
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool

	// 連接獲取
	GetConnection() (*eslgo.Conn, error)

	// 健康檢查
	HealthCheck(ctx context.Context) error
	GetHealthStatus() ConnectionStatus
	TriggerHealthCheck() error

	// 事件監聽
	RegisterEventListener(eventType string, handler EventHandler) (string, error)
	RemoveEventListener(eventType string, listenerID string) error
	GetEventListenerCount(eventType string) int
	GetRegisteredEventTypes() []string
}

// ESLConfig 定義ESL配置結構
type ESLConfig struct {
	Host                 string        `toml:"host"`
	Password             string        `toml:"password"`
	ReconnectInterval    time.Duration `toml:"reconnect_interval"`
	HealthCheckInterval  time.Duration `toml:"health_check_interval"`
	MaxReconnectAttempts int           `toml:"max_reconnect_attempts"`
}

// ConnectionStatus 追蹤連接狀態
type ConnectionStatus struct {
	Connected           bool             `json:"connected"`
	LastConnected       time.Time        `json:"last_connected"`
	LastError           string           `json:"last_error,omitempty"`
	LastErrorCode       ErrorCode        `json:"last_error_code,omitempty"`
	ReconnectCount      int              `json:"reconnect_count"`
	LastHealthCheck     time.Time        `json:"last_health_check"`
	HealthCheckPassed   bool             `json:"health_check_passed"`
	Uptime              time.Duration    `json:"uptime"`
	TotalConnections    int64            `json:"total_connections"`
	TotalReconnects     int64            `json:"total_reconnects"`
	TotalHealthChecks   int64            `json:"total_health_checks"`
	CircuitBreakerState string           `json:"circuit_breaker_state"`
	RecoveryStrategy    RecoveryStrategy `json:"recovery_strategy"`
}

// ConnectionManager 實現ESLManager接口
type ConnectionManager struct {
	config    *ESLConfig
	conn      *eslgo.Conn
	mu        sync.RWMutex
	connected bool
	status    ConnectionStatus

	// 事件處理
	eventDispatcher *EventDispatcher

	// 重連控制
	reconnectCtx    context.Context
	reconnectCancel context.CancelFunc
	reconnectMu     sync.Mutex

	// 健康檢查
	healthCheckTicker *time.Ticker
	healthCheckDone   chan struct{}
	healthCheckMu     sync.Mutex

	// 錯誤恢復
	recoveryConfig *ErrorRecoveryConfig
	circuitBreaker *CircuitBreaker

	// 日誌記錄器
	logger *logger.Logger

	// 監控指標
	startTime time.Time
}

// NewConnectionManager 創建新的連接管理器
func NewConnectionManager(config *ESLConfig) *ConnectionManager {
	if config == nil {
		config = &ESLConfig{}
	}

	// 設置預設值
	if config.Host == "" {
		config.Host = "localhost:8021"
	}
	if config.Password == "" {
		config.Password = "ClueCon"
	}
	if config.ReconnectInterval == 0 {
		config.ReconnectInterval = 5 * time.Second
	}
	if config.HealthCheckInterval == 0 {
		config.HealthCheckInterval = 30 * time.Second
	}
	if config.MaxReconnectAttempts == 0 {
		config.MaxReconnectAttempts = 10
	}

	// 驗證健康檢查間隔的合理性
	if config.HealthCheckInterval < 5*time.Second {
		log.Printf("Warning: HealthCheckInterval too short (%v), setting to minimum 5s", config.HealthCheckInterval)
		config.HealthCheckInterval = 5 * time.Second
	}
	if config.HealthCheckInterval > 5*time.Minute {
		log.Printf("Warning: HealthCheckInterval too long (%v), setting to maximum 5m", config.HealthCheckInterval)
		config.HealthCheckInterval = 5 * time.Minute
	}

	// 創建錯誤恢復配置
	recoveryConfig := &ErrorRecoveryConfig{
		MaxRetries:              config.MaxReconnectAttempts,
		RetryInterval:           config.ReconnectInterval,
		CircuitBreakerThreshold: 5,               // 5次失敗後開啟熔斷器
		CircuitBreakerTimeout:   1 * time.Minute, // 1分鐘後嘗試半開
		EnableAutoRecovery:      true,
	}

	// 創建熔斷器
	circuitBreaker := NewCircuitBreaker(
		recoveryConfig.CircuitBreakerThreshold,
		recoveryConfig.CircuitBreakerTimeout,
	)

	cm := &ConnectionManager{
		config:          config,
		eventDispatcher: NewEventDispatcher(),
		healthCheckDone: make(chan struct{}),
		recoveryConfig:  recoveryConfig,
		circuitBreaker:  circuitBreaker,
		logger:          logger.GetDefaultLogger(),
		startTime:       time.Now(),
	}

	// 初始化狀態
	cm.status.CircuitBreakerState = circuitBreaker.GetState().String()
	cm.status.RecoveryStrategy = RecoveryStrategyReconnect

	// 記錄配置驗證
	cm.logger.LogConfigValidation(true, nil)

	return cm
}

// Connect 建立ESL連接
func (cm *ConnectionManager) Connect(ctx context.Context) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.connected && cm.conn != nil {
		return nil // 已經連接
	}

	startTime := time.Now()
	attempt := int(cm.status.TotalConnections + 1)

	// 記錄連接嘗試
	cm.logger.LogConnectionAttempt(cm.config.Host, attempt)

	// 使用熔斷器保護連接操作
	err := cm.circuitBreaker.Call(func() error {
		return cm.doConnect(ctx)
	})

	duration := time.Since(startTime)
	cm.status.TotalConnections++

	if err != nil {
		// 創建詳細的錯誤信息
		eslErr := NewESLError("connect", cm.config.Host, err, ErrorCodeConnection, map[string]interface{}{
			"attempt":  attempt,
			"duration": duration,
		})

		cm.status.LastError = eslErr.Error()
		cm.status.LastErrorCode = eslErr.Code
		cm.status.CircuitBreakerState = cm.circuitBreaker.GetState().String()

		// 記錄連接失敗
		cm.logger.LogConnectionFailed(cm.config.Host, eslErr, duration)

		return eslErr
	}

	// 連接成功
	cm.status.Connected = true
	cm.status.LastConnected = time.Now()
	cm.status.LastError = ""
	cm.status.LastErrorCode = 0
	cm.status.CircuitBreakerState = cm.circuitBreaker.GetState().String()

	// 記錄連接成功
	cm.logger.LogConnectionSuccess(cm.config.Host, duration)

	// 啟動健康檢查
	cm.startHealthCheck()

	return nil
}

// doConnect 執行實際的連接操作
func (cm *ConnectionManager) doConnect(ctx context.Context) error {
	// 設置連接超時
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// 創建連接通道
	connChan := make(chan *eslgo.Conn, 1)
	errChan := make(chan error, 1)

	// 異步建立連接
	go func() {
		conn, err := eslgo.Dial(cm.config.Host, cm.config.Password, func() {
			cm.handleConnectionLost()
		})
		if err != nil {
			errChan <- err
			return
		}
		connChan <- conn
	}()

	// 等待連接結果或超時
	select {
	case conn := <-connChan:
		// 啟用事件接收
		if err := conn.EnableEvents(connectCtx); err != nil {
			conn.ExitAndClose()
			return fmt.Errorf("failed to enable events: %w", err)
		}

		// 註冊事件分發處理器
		conn.RegisterEventListener(eslgo.EventListenAll, cm.eventDispatcher.DispatchEvent)

		cm.conn = conn
		cm.connected = true
		return nil

	case err := <-errChan:
		return fmt.Errorf("connection failed: %w", err)

	case <-connectCtx.Done():
		return NewESLError("connect", cm.config.Host,
			ErrESLConnectionTimeout, ErrorCodeTimeout,
			map[string]interface{}{"timeout": "30s"})
	}
}

// Disconnect 斷開ESL連接
func (cm *ConnectionManager) Disconnect() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if !cm.connected && cm.conn == nil {
		return nil // 已經斷開
	}

	// 計算連接時長
	var uptime time.Duration
	if !cm.status.LastConnected.IsZero() {
		uptime = time.Since(cm.status.LastConnected)
	}

	// 停止重連
	cm.stopReconnect()

	// 停止健康檢查
	cm.stopHealthCheck()

	// 關閉連接
	if cm.conn != nil {
		cm.conn.ExitAndClose()
		cm.conn = nil
	}

	cm.connected = false
	cm.status.Connected = false
	cm.status.Uptime = uptime

	// 記錄斷開連接
	cm.logger.LogESLEvent(logger.ESLEventConnectionLost, "ESL connection disconnected gracefully",
		slog.String("host", cm.config.Host),
		slog.Duration("uptime", uptime),
		slog.String("reason", "manual_disconnect"),
	)

	// 更新狀態
	cm.logger.LogStatusUpdate(cm.status)

	return nil
}

// IsConnected 檢查是否已連接
func (cm *ConnectionManager) IsConnected() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.connected && cm.conn != nil
}

// GetConnection 獲取ESL連接
func (cm *ConnectionManager) GetConnection() (*eslgo.Conn, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if !cm.connected || cm.conn == nil {
		return nil, ErrESLNotConnected
	}

	return cm.conn, nil
}

// HealthCheck 執行健康檢查
func (cm *ConnectionManager) HealthCheck(ctx context.Context) error {
	startTime := time.Now()

	// 記錄健康檢查開始
	cm.logger.LogHealthCheckStart()

	conn, err := cm.GetConnection()
	if err != nil {
		duration := time.Since(startTime)
		eslErr := NewESLError("health_check", cm.config.Host, err, ErrorCodeHealthCheck, map[string]interface{}{
			"reason":   "no_connection",
			"duration": duration,
		})
		cm.logger.LogHealthCheckFailed(eslErr, duration)
		return eslErr
	}

	// 使用帶超時的上下文進行健康檢查
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 執行健康檢查操作
	err = cm.performHealthCheckOperations(checkCtx, conn)
	duration := time.Since(startTime)

	if err != nil {
		eslErr := NewESLError("health_check", cm.config.Host, err, ErrorCodeHealthCheck, map[string]interface{}{
			"duration": duration,
		})
		cm.logger.LogHealthCheckFailed(eslErr, duration)
		return eslErr
	}

	// 健康檢查成功
	cm.logger.LogHealthCheckSuccess(duration)
	return nil
}

// performHealthCheckOperations 執行健康檢查操作
func (cm *ConnectionManager) performHealthCheckOperations(ctx context.Context, conn *eslgo.Conn) error {
	// 檢查連接是否仍然有效
	if conn == nil {
		return errors.New("connection is nil")
	}

	// 創建測試事件處理器
	testHandler := func(event *eslgo.Event) {
		// 測試處理器，不做任何事情
	}

	// 嘗試註冊一個測試監聽器
	listenerID := conn.RegisterEventListener("HEARTBEAT", testHandler)
	if listenerID == "" {
		return errors.New("unable to register event listener")
	}

	// 立即移除測試監聽器
	conn.RemoveEventListener("HEARTBEAT", listenerID)

	// 檢查上下文是否已被取消（超時）
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return errors.New("health check timeout")
		}
		return ctx.Err()
	default:
		// 繼續執行
	}

	// 可以添加更多健康檢查操作，例如：
	// - 發送ping命令
	// - 檢查事件接收是否正常
	// - 驗證認證狀態

	return nil
}

// GetHealthStatus 獲取當前健康狀態
func (cm *ConnectionManager) GetHealthStatus() ConnectionStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 更新運行時間
	if cm.connected && !cm.status.LastConnected.IsZero() {
		cm.status.Uptime = time.Since(cm.status.LastConnected)
	} else {
		cm.status.Uptime = 0
	}

	// 更新熔斷器狀態
	cm.status.CircuitBreakerState = cm.circuitBreaker.GetState().String()

	// 獲取連接監控指標
	metrics := logger.GetConnectionMetrics()
	cm.status.TotalConnections = metrics.TotalConnections
	cm.status.TotalReconnects = metrics.TotalReconnects
	cm.status.TotalHealthChecks = metrics.TotalHealthChecks

	return cm.status
}

// TriggerHealthCheck 手動觸發健康檢查
func (cm *ConnectionManager) TriggerHealthCheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	checkTime := time.Now()
	err := cm.HealthCheck(ctx)

	// 更新健康檢查狀態
	cm.mu.Lock()
	cm.status.LastHealthCheck = checkTime
	cm.status.HealthCheckPassed = (err == nil)
	cm.status.TotalHealthChecks++

	if err != nil {
		if eslErr, ok := err.(*ESLError); ok {
			cm.status.LastError = eslErr.Error()
			cm.status.LastErrorCode = eslErr.Code
		} else {
			cm.status.LastError = fmt.Sprintf("Manual health check failed: %v", err)
			cm.status.LastErrorCode = ErrorCodeHealthCheck
		}
	} else {
		// 健康檢查成功，清除錯誤狀態
		cm.status.LastError = ""
		cm.status.LastErrorCode = 0
	}
	cm.mu.Unlock()

	// 記錄狀態更新
	cm.logger.LogStatusUpdate(cm.GetHealthStatus())

	return err
}

// RegisterEventListener 註冊事件監聽器
func (cm *ConnectionManager) RegisterEventListener(eventType string, handler EventHandler) (string, error) {
	return cm.eventDispatcher.RegisterListener(eventType, handler)
}

// RemoveEventListener 移除事件監聽器
func (cm *ConnectionManager) RemoveEventListener(eventType string, listenerID string) error {
	return cm.eventDispatcher.RemoveListener(eventType, listenerID)
}

// GetEventListenerCount 獲取指定事件類型的監聽器數量
func (cm *ConnectionManager) GetEventListenerCount(eventType string) int {
	return cm.eventDispatcher.GetListenerCount(eventType)
}

// GetRegisteredEventTypes 獲取所有已註冊的事件類型
func (cm *ConnectionManager) GetRegisteredEventTypes() []string {
	return cm.eventDispatcher.GetAllEventTypes()
}

// handleConnectionLost 處理連接丟失
func (cm *ConnectionManager) handleConnectionLost() {
	cm.mu.Lock()

	// 計算連接時長
	var uptime time.Duration
	if !cm.status.LastConnected.IsZero() {
		uptime = time.Since(cm.status.LastConnected)
	}

	cm.connected = false
	cm.status.Connected = false
	cm.status.LastError = "Connection lost unexpectedly"
	cm.status.LastErrorCode = ErrorCodeConnection
	cm.status.Uptime = uptime

	cm.mu.Unlock()

	// 記錄連接丟失
	cm.logger.LogConnectionLost(cm.config.Host, uptime)

	// 檢查是否啟用自動恢復
	if cm.recoveryConfig.EnableAutoRecovery {
		// 根據恢復策略決定行動
		switch cm.status.RecoveryStrategy {
		case RecoveryStrategyReconnect:
			cm.logger.LogESLEvent(logger.ESLEventConnectionLost, "Starting automatic reconnection",
				slog.String("strategy", "reconnect"),
			)
			cm.startReconnect()
		case RecoveryStrategyCircuitBreaker:
			cm.logger.LogESLEvent(logger.ESLEventConnectionLost, "Circuit breaker activated",
				slog.String("strategy", "circuit_breaker"),
			)
			// 熔斷器會在下次連接嘗試時生效
			cm.startReconnect()
		default:
			cm.logger.LogESLEvent(logger.ESLEventConnectionLost, "No automatic recovery configured",
				slog.String("strategy", "none"),
			)
		}
	} else {
		cm.logger.LogESLEvent(logger.ESLEventConnectionLost, "Automatic recovery disabled",
			slog.Bool("auto_recovery", false),
		)
	}

	// 更新狀態
	cm.logger.LogStatusUpdate(cm.GetHealthStatus())
}

// startReconnect 啟動重連機制
func (cm *ConnectionManager) startReconnect() {
	cm.reconnectMu.Lock()
	defer cm.reconnectMu.Unlock()

	// 如果已經在重連中，不要重複啟動
	if cm.reconnectCtx != nil {
		return
	}

	cm.reconnectCtx, cm.reconnectCancel = context.WithCancel(context.Background())
	go cm.reconnectLoop()
}

// stopReconnect 停止重連機制
func (cm *ConnectionManager) stopReconnect() {
	cm.reconnectMu.Lock()
	defer cm.reconnectMu.Unlock()

	if cm.reconnectCancel != nil {
		cm.reconnectCancel()
		cm.reconnectCancel = nil
		cm.reconnectCtx = nil
	}
}

// reconnectLoop 重連循環
func (cm *ConnectionManager) reconnectLoop() {
	backoff := cm.config.ReconnectInterval
	maxBackoff := 30 * time.Second
	attempts := 0
	startTime := time.Now()

	for {
		// 檢查重連上下文是否仍然有效
		cm.reconnectMu.Lock()
		ctx := cm.reconnectCtx
		cm.reconnectMu.Unlock()

		if ctx == nil {
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			attempts++
			cm.mu.Lock()
			cm.status.ReconnectCount = attempts
			cm.status.TotalReconnects++
			cm.mu.Unlock()

			// 記錄重連嘗試
			cm.logger.LogReconnectAttempt(cm.config.Host, attempts, cm.config.MaxReconnectAttempts, backoff)

			// 嘗試重連
			err := cm.Connect(context.Background())
			if err != nil {
				// 記錄重連失敗
				cm.logger.LogReconnectFailed(cm.config.Host, attempts, err)

				if attempts >= cm.config.MaxReconnectAttempts {
					totalDuration := time.Since(startTime)

					// 記錄放棄重連
					cm.logger.LogReconnectGiveUp(cm.config.Host, attempts, totalDuration)

					// 更新狀態
					cm.mu.Lock()
					cm.status.LastError = fmt.Sprintf("Max reconnect attempts reached after %d tries", attempts)
					cm.status.LastErrorCode = ErrorCodeReconnection
					cm.status.RecoveryStrategy = RecoveryStrategyNone
					cm.mu.Unlock()

					// 記錄最終狀態
					cm.logger.LogStatusUpdate(cm.GetHealthStatus())
					return
				}

				// 指數退避
				backoff = min(backoff*2, maxBackoff)
				continue
			}

			// 重連成功
			totalDuration := time.Since(startTime)
			cm.logger.LogReconnectSuccess(cm.config.Host, attempts, totalDuration)

			// 重置狀態
			cm.mu.Lock()
			cm.status.ReconnectCount = 0
			cm.status.LastError = ""
			cm.status.LastErrorCode = 0
			cm.status.RecoveryStrategy = RecoveryStrategyReconnect
			cm.mu.Unlock()

			// 重置退避時間
			backoff = cm.config.ReconnectInterval

			// 重置熔斷器
			cm.circuitBreaker.Reset()

			// 記錄狀態更新
			cm.logger.LogStatusUpdate(cm.GetHealthStatus())

			// 停止重連循環
			cm.stopReconnect()
			return
		}
	}
}

// startHealthCheck 啟動健康檢查
func (cm *ConnectionManager) startHealthCheck() {
	cm.healthCheckMu.Lock()
	defer cm.healthCheckMu.Unlock()

	// 停止現有的健康檢查
	cm.stopHealthCheckUnsafe()

	cm.healthCheckTicker = time.NewTicker(cm.config.HealthCheckInterval)
	cm.healthCheckDone = make(chan struct{})

	go cm.healthCheckLoop()
}

// stopHealthCheck 停止健康檢查
func (cm *ConnectionManager) stopHealthCheck() {
	cm.healthCheckMu.Lock()
	defer cm.healthCheckMu.Unlock()
	cm.stopHealthCheckUnsafe()
}

// stopHealthCheckUnsafe 停止健康檢查（不加鎖版本）
func (cm *ConnectionManager) stopHealthCheckUnsafe() {
	if cm.healthCheckTicker != nil {
		cm.healthCheckTicker.Stop()
		cm.healthCheckTicker = nil
	}

	if cm.healthCheckDone != nil {
		select {
		case <-cm.healthCheckDone:
			// 通道已經關閉
		default:
			close(cm.healthCheckDone)
		}
		cm.healthCheckDone = nil
	}
}

// healthCheckLoop 健康檢查循環
func (cm *ConnectionManager) healthCheckLoop() {
	log.Printf("Starting health check loop with interval: %v", cm.config.HealthCheckInterval)

	// 獲取當前的ticker和done channel
	cm.healthCheckMu.Lock()
	ticker := cm.healthCheckTicker
	done := cm.healthCheckDone
	cm.healthCheckMu.Unlock()

	if ticker == nil || done == nil {
		log.Printf("Health check loop stopped: ticker or done channel is nil")
		return
	}

	for {
		select {
		case <-done:
			log.Printf("Health check loop stopped")
			return
		case <-ticker.C:
			cm.performHealthCheck()
		}
	}
}

// performHealthCheck 執行單次健康檢查並處理失敗情況
func (cm *ConnectionManager) performHealthCheck() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	checkTime := time.Now()
	err := cm.HealthCheck(ctx)

	// 更新健康檢查狀態
	cm.mu.Lock()
	cm.status.LastHealthCheck = checkTime
	cm.status.HealthCheckPassed = (err == nil)
	cm.status.TotalHealthChecks++
	cm.mu.Unlock()

	if err != nil {
		// 健康檢查失敗處理
		cm.mu.Lock()
		cm.connected = false
		cm.status.Connected = false

		if eslErr, ok := err.(*ESLError); ok {
			cm.status.LastError = eslErr.Error()
			cm.status.LastErrorCode = eslErr.Code
		} else {
			cm.status.LastError = fmt.Sprintf("Health check failed: %v", err)
			cm.status.LastErrorCode = ErrorCodeHealthCheck
		}
		cm.mu.Unlock()

		// 記錄健康檢查失敗並觸發恢復機制
		cm.logger.LogESLEvent(logger.ESLEventHealthCheckFailed, "Health check failure detected, triggering recovery",
			slog.String("error", err.Error()),
			slog.String("recovery_action", "reconnect"),
		)

		// 觸發自動重連
		cm.handleConnectionLost()
	} else {
		// 健康檢查成功，確保狀態正確
		cm.mu.Lock()
		if cm.conn != nil {
			cm.connected = true
			cm.status.Connected = true
			cm.status.LastError = ""
			cm.status.LastErrorCode = 0
		}
		cm.mu.Unlock()

		// 記錄健康檢查成功（僅在調試模式下）
		cm.logger.LogESLEvent(logger.ESLEventHealthCheckSuccess, "Periodic health check passed",
			slog.Time("check_time", checkTime),
		)
	}

	// 記錄狀態更新
	cm.logger.LogStatusUpdate(cm.GetHealthStatus())
}

// CircuitBreaker 實現熔斷器模式
type CircuitBreaker struct {
	mu           sync.RWMutex
	state        CircuitBreakerState
	failures     int
	threshold    int
	timeout      time.Duration
	lastFailTime time.Time
}

// CircuitBreakerState 熔斷器狀態
type CircuitBreakerState int

const (
	CircuitBreakerClosed CircuitBreakerState = iota
	CircuitBreakerOpen
	CircuitBreakerHalfOpen
)

// String 返回熔斷器狀態的字符串表示
func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitBreakerClosed:
		return "closed"
	case CircuitBreakerOpen:
		return "open"
	case CircuitBreakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// NewCircuitBreaker 創建新的熔斷器
func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:     CircuitBreakerClosed,
		threshold: threshold,
		timeout:   timeout,
	}
}

// Call 執行操作，如果熔斷器開啟則直接返回錯誤
func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mu.RLock()
	state := cb.state
	cb.mu.RUnlock()

	if state == CircuitBreakerOpen {
		cb.mu.RLock()
		canRetry := time.Since(cb.lastFailTime) > cb.timeout
		cb.mu.RUnlock()

		if canRetry {
			cb.mu.Lock()
			cb.state = CircuitBreakerHalfOpen
			cb.mu.Unlock()
		} else {
			return errors.New("circuit breaker is open")
		}
	}

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailTime = time.Now()

		if cb.failures >= cb.threshold {
			cb.state = CircuitBreakerOpen
		}
		return err
	}

	// 成功時重置
	cb.failures = 0
	cb.state = CircuitBreakerClosed
	return nil
}

// GetState 獲取熔斷器狀態
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset 重置熔斷器
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.state = CircuitBreakerClosed
}

// min 返回兩個duration中較小的值
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
