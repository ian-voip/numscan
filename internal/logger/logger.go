package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Logger 包裝 slog.Logger 提供結構化日誌記錄
type Logger struct {
	*slog.Logger
	level slog.Level
}

// LoggingConfig 日誌配置結構
type LoggingConfig struct {
	Level      string `koanf:"level"`
	Format     string `koanf:"format"`
	Output     string `koanf:"output"`
	FilePath   string `koanf:"file_path"`
	MaxSize    int    `koanf:"max_size"`
	MaxBackups int    `koanf:"max_backups"`
	MaxAge     int    `koanf:"max_age"`
	Compress   bool   `koanf:"compress"`
}

// ESLEvent 定義ESL相關的日誌事件類型
type ESLEvent string

const (
	ESLEventConnectionAttempt  ESLEvent = "connection_attempt"
	ESLEventConnectionSuccess  ESLEvent = "connection_success"
	ESLEventConnectionFailed   ESLEvent = "connection_failed"
	ESLEventConnectionLost     ESLEvent = "connection_lost"
	ESLEventReconnectAttempt   ESLEvent = "reconnect_attempt"
	ESLEventReconnectSuccess   ESLEvent = "reconnect_success"
	ESLEventReconnectFailed    ESLEvent = "reconnect_failed"
	ESLEventReconnectGiveUp    ESLEvent = "reconnect_give_up"
	ESLEventHealthCheckStart   ESLEvent = "health_check_start"
	ESLEventHealthCheckSuccess ESLEvent = "health_check_success"
	ESLEventHealthCheckFailed  ESLEvent = "health_check_failed"
	ESLEventListenerRegistered ESLEvent = "listener_registered"
	ESLEventListenerRemoved    ESLEvent = "listener_removed"
	ESLEventDispatchStart      ESLEvent = "event_dispatch_start"
	ESLEventDispatchSuccess    ESLEvent = "event_dispatch_success"
	ESLEventDispatchFailed     ESLEvent = "event_dispatch_failed"
	ESLEventConfigValidation   ESLEvent = "config_validation"
	ESLEventStatusUpdate       ESLEvent = "status_update"
)

// ConnectionMetrics 連接監控指標
type ConnectionMetrics struct {
	TotalConnections       int64         `json:"total_connections"`
	SuccessfulConnections  int64         `json:"successful_connections"`
	FailedConnections      int64         `json:"failed_connections"`
	TotalReconnects        int64         `json:"total_reconnects"`
	SuccessfulReconnects   int64         `json:"successful_reconnects"`
	FailedReconnects       int64         `json:"failed_reconnects"`
	TotalHealthChecks      int64         `json:"total_health_checks"`
	SuccessfulHealthChecks int64         `json:"successful_health_checks"`
	FailedHealthChecks     int64         `json:"failed_health_checks"`
	CurrentUptime          time.Duration `json:"current_uptime"`
	LastConnectionTime     time.Time     `json:"last_connection_time"`
	LastDisconnectionTime  time.Time     `json:"last_disconnection_time"`
}

var (
	defaultLogger *Logger
	metrics       ConnectionMetrics
)

// NewLogger 創建新的結構化日誌記錄器
func NewLogger(config *LoggingConfig) (*Logger, error) {
	if config == nil {
		config = &LoggingConfig{
			Level:  "info",
			Format: "text",
			Output: "stdout",
		}
	}

	// 解析日誌級別
	level, err := parseLogLevel(config.Level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}

	// 創建輸出目標
	writer, err := createWriter(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create log writer: %w", err)
	}

	// 創建處理器
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug,
	}

	switch strings.ToLower(config.Format) {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	case "text":
		handler = slog.NewTextHandler(writer, opts)
	default:
		return nil, fmt.Errorf("unsupported log format: %s", config.Format)
	}

	logger := &Logger{
		Logger: slog.New(handler),
		level:  level,
	}

	return logger, nil
}

// InitDefaultLogger 初始化默認日誌記錄器
func InitDefaultLogger(config *LoggingConfig) error {
	logger, err := NewLogger(config)
	if err != nil {
		return err
	}
	defaultLogger = logger
	return nil
}

// GetDefaultLogger 獲取默認日誌記錄器
func GetDefaultLogger() *Logger {
	if defaultLogger == nil {
		// 如果沒有初始化，創建一個基本的日誌記錄器
		logger, _ := NewLogger(nil)
		defaultLogger = logger
	}
	return defaultLogger
}

// parseLogLevel 解析日誌級別字符串
func parseLogLevel(levelStr string) (slog.Level, error) {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level: %s", levelStr)
	}
}

// createWriter 根據配置創建日誌輸出目標
func createWriter(config *LoggingConfig) (io.Writer, error) {
	switch strings.ToLower(config.Output) {
	case "stdout":
		return os.Stdout, nil
	case "stderr":
		return os.Stderr, nil
	case "file":
		if config.FilePath == "" {
			return nil, fmt.Errorf("file path is required when output is 'file'")
		}

		// 確保日誌目錄存在
		dir := filepath.Dir(config.FilePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// 使用 lumberjack 進行日誌輪轉
		return &lumberjack.Logger{
			Filename:   config.FilePath,
			MaxSize:    config.MaxSize,
			MaxBackups: config.MaxBackups,
			MaxAge:     config.MaxAge,
			Compress:   config.Compress,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported output type: %s", config.Output)
	}
}

// ESL相關的結構化日誌方法

// LogESLEvent 記錄ESL事件
func (l *Logger) LogESLEvent(event ESLEvent, message string, attrs ...slog.Attr) {
	baseAttrs := []slog.Attr{
		slog.String("component", "esl"),
		slog.String("event", string(event)),
		slog.Time("timestamp", time.Now()),
	}

	allAttrs := append(baseAttrs, attrs...)

	// 轉換 slog.Attr 到 any
	anyAttrs := make([]any, len(allAttrs))
	for i, attr := range allAttrs {
		anyAttrs[i] = attr
	}

	switch event {
	case ESLEventConnectionFailed, ESLEventReconnectFailed, ESLEventHealthCheckFailed, ESLEventDispatchFailed, ESLEventReconnectGiveUp:
		l.Error(message, anyAttrs...)
	case ESLEventConnectionAttempt, ESLEventReconnectAttempt, ESLEventHealthCheckStart:
		l.Info(message, anyAttrs...)
	case ESLEventConnectionSuccess, ESLEventReconnectSuccess, ESLEventHealthCheckSuccess:
		l.Info(message, anyAttrs...)
	default:
		l.Debug(message, anyAttrs...)
	}
}

// LogConnectionAttempt 記錄連接嘗試
func (l *Logger) LogConnectionAttempt(host string, attempt int) {
	metrics.TotalConnections++
	l.LogESLEvent(ESLEventConnectionAttempt, "Attempting ESL connection",
		slog.String("host", host),
		slog.Int("attempt", attempt),
		slog.Int64("total_connections", metrics.TotalConnections),
	)
}

// LogConnectionSuccess 記錄連接成功
func (l *Logger) LogConnectionSuccess(host string, duration time.Duration) {
	metrics.SuccessfulConnections++
	metrics.LastConnectionTime = time.Now()
	l.LogESLEvent(ESLEventConnectionSuccess, "ESL connection established successfully",
		slog.String("host", host),
		slog.Duration("connection_time", duration),
		slog.Int64("successful_connections", metrics.SuccessfulConnections),
		slog.Time("connected_at", metrics.LastConnectionTime),
	)
}

// LogConnectionFailed 記錄連接失敗
func (l *Logger) LogConnectionFailed(host string, err error, duration time.Duration) {
	metrics.FailedConnections++
	l.LogESLEvent(ESLEventConnectionFailed, "ESL connection failed",
		slog.String("host", host),
		slog.String("error", err.Error()),
		slog.Duration("attempt_duration", duration),
		slog.Int64("failed_connections", metrics.FailedConnections),
	)
}

// LogConnectionLost 記錄連接丟失
func (l *Logger) LogConnectionLost(host string, uptime time.Duration) {
	metrics.LastDisconnectionTime = time.Now()
	l.LogESLEvent(ESLEventConnectionLost, "ESL connection lost",
		slog.String("host", host),
		slog.Duration("uptime", uptime),
		slog.Time("disconnected_at", metrics.LastDisconnectionTime),
	)
}

// LogReconnectAttempt 記錄重連嘗試
func (l *Logger) LogReconnectAttempt(host string, attempt int, maxAttempts int, backoff time.Duration) {
	metrics.TotalReconnects++
	l.LogESLEvent(ESLEventReconnectAttempt, "Attempting ESL reconnection",
		slog.String("host", host),
		slog.Int("attempt", attempt),
		slog.Int("max_attempts", maxAttempts),
		slog.Duration("backoff", backoff),
		slog.Int64("total_reconnects", metrics.TotalReconnects),
	)
}

// LogReconnectSuccess 記錄重連成功
func (l *Logger) LogReconnectSuccess(host string, attempts int, totalDuration time.Duration) {
	metrics.SuccessfulReconnects++
	metrics.LastConnectionTime = time.Now()
	l.LogESLEvent(ESLEventReconnectSuccess, "ESL reconnection successful",
		slog.String("host", host),
		slog.Int("attempts_taken", attempts),
		slog.Duration("total_reconnect_time", totalDuration),
		slog.Int64("successful_reconnects", metrics.SuccessfulReconnects),
		slog.Time("reconnected_at", metrics.LastConnectionTime),
	)
}

// LogReconnectFailed 記錄重連失敗
func (l *Logger) LogReconnectFailed(host string, attempt int, err error) {
	metrics.FailedReconnects++
	l.LogESLEvent(ESLEventReconnectFailed, "ESL reconnection attempt failed",
		slog.String("host", host),
		slog.Int("attempt", attempt),
		slog.String("error", err.Error()),
		slog.Int64("failed_reconnects", metrics.FailedReconnects),
	)
}

// LogReconnectGiveUp 記錄放棄重連
func (l *Logger) LogReconnectGiveUp(host string, totalAttempts int, totalDuration time.Duration) {
	l.LogESLEvent(ESLEventReconnectGiveUp, "Giving up ESL reconnection after max attempts",
		slog.String("host", host),
		slog.Int("total_attempts", totalAttempts),
		slog.Duration("total_duration", totalDuration),
		slog.String("severity", "critical"),
	)
}

// LogHealthCheckStart 記錄健康檢查開始
func (l *Logger) LogHealthCheckStart() {
	metrics.TotalHealthChecks++
	l.LogESLEvent(ESLEventHealthCheckStart, "Starting ESL health check",
		slog.Int64("total_health_checks", metrics.TotalHealthChecks),
	)
}

// LogHealthCheckSuccess 記錄健康檢查成功
func (l *Logger) LogHealthCheckSuccess(duration time.Duration) {
	metrics.SuccessfulHealthChecks++
	l.LogESLEvent(ESLEventHealthCheckSuccess, "ESL health check passed",
		slog.Duration("check_duration", duration),
		slog.Int64("successful_health_checks", metrics.SuccessfulHealthChecks),
	)
}

// LogHealthCheckFailed 記錄健康檢查失敗
func (l *Logger) LogHealthCheckFailed(err error, duration time.Duration) {
	metrics.FailedHealthChecks++
	l.LogESLEvent(ESLEventHealthCheckFailed, "ESL health check failed",
		slog.String("error", err.Error()),
		slog.Duration("check_duration", duration),
		slog.Int64("failed_health_checks", metrics.FailedHealthChecks),
	)
}

// LogEventListenerRegistered 記錄事件監聽器註冊
func (l *Logger) LogEventListenerRegistered(eventType string, listenerID string, totalListeners int) {
	l.LogESLEvent(ESLEventListenerRegistered, "Event listener registered",
		slog.String("event_type", eventType),
		slog.String("listener_id", listenerID),
		slog.Int("total_listeners", totalListeners),
	)
}

// LogEventListenerRemoved 記錄事件監聽器移除
func (l *Logger) LogEventListenerRemoved(eventType string, listenerID string, totalListeners int) {
	l.LogESLEvent(ESLEventListenerRemoved, "Event listener removed",
		slog.String("event_type", eventType),
		slog.String("listener_id", listenerID),
		slog.Int("total_listeners", totalListeners),
	)
}

// LogEventDispatch 記錄事件分發
func (l *Logger) LogEventDispatch(eventName string, listenersCount int, success bool, err error) {
	if success {
		l.LogESLEvent(ESLEventDispatchSuccess, "Event dispatched successfully",
			slog.String("event_name", eventName),
			slog.Int("listeners_count", listenersCount),
		)
	} else {
		l.LogESLEvent(ESLEventDispatchFailed, "Event dispatch failed",
			slog.String("event_name", eventName),
			slog.Int("listeners_count", listenersCount),
			slog.String("error", err.Error()),
		)
	}
}

// LogConfigValidation 記錄配置驗證
func (l *Logger) LogConfigValidation(valid bool, errors []string) {
	if valid {
		l.LogESLEvent(ESLEventConfigValidation, "ESL configuration validation passed")
	} else {
		l.LogESLEvent(ESLEventConfigValidation, "ESL configuration validation failed",
			slog.Any("validation_errors", errors),
		)
	}
}

// LogStatusUpdate 記錄狀態更新
func (l *Logger) LogStatusUpdate(status interface{}) {
	l.LogESLEvent(ESLEventStatusUpdate, "ESL connection status updated",
		slog.Any("status", status),
	)
}

// GetConnectionMetrics 獲取連接監控指標
func GetConnectionMetrics() ConnectionMetrics {
	if !metrics.LastConnectionTime.IsZero() && metrics.LastDisconnectionTime.Before(metrics.LastConnectionTime) {
		metrics.CurrentUptime = time.Since(metrics.LastConnectionTime)
	} else {
		metrics.CurrentUptime = 0
	}
	return metrics
}

// ResetConnectionMetrics 重置連接監控指標
func ResetConnectionMetrics() {
	metrics = ConnectionMetrics{}
}

// 便利方法，使用默認日誌記錄器

// LogESLEvent 使用默認日誌記錄器記錄ESL事件
func LogESLEvent(event ESLEvent, message string, attrs ...slog.Attr) {
	GetDefaultLogger().LogESLEvent(event, message, attrs...)
}

// LogConnectionAttempt 使用默認日誌記錄器記錄連接嘗試
func LogConnectionAttempt(host string, attempt int) {
	GetDefaultLogger().LogConnectionAttempt(host, attempt)
}

// LogConnectionSuccess 使用默認日誌記錄器記錄連接成功
func LogConnectionSuccess(host string, duration time.Duration) {
	GetDefaultLogger().LogConnectionSuccess(host, duration)
}

// LogConnectionFailed 使用默認日誌記錄器記錄連接失敗
func LogConnectionFailed(host string, err error, duration time.Duration) {
	GetDefaultLogger().LogConnectionFailed(host, err, duration)
}

// LogConnectionLost 使用默認日誌記錄器記錄連接丟失
func LogConnectionLost(host string, uptime time.Duration) {
	GetDefaultLogger().LogConnectionLost(host, uptime)
}

// LogReconnectAttempt 使用默認日誌記錄器記錄重連嘗試
func LogReconnectAttempt(host string, attempt int, maxAttempts int, backoff time.Duration) {
	GetDefaultLogger().LogReconnectAttempt(host, attempt, maxAttempts, backoff)
}

// LogReconnectSuccess 使用默認日誌記錄器記錄重連成功
func LogReconnectSuccess(host string, attempts int, totalDuration time.Duration) {
	GetDefaultLogger().LogReconnectSuccess(host, attempts, totalDuration)
}

// LogReconnectFailed 使用默認日誌記錄器記錄重連失敗
func LogReconnectFailed(host string, attempt int, err error) {
	GetDefaultLogger().LogReconnectFailed(host, attempt, err)
}

// LogReconnectGiveUp 使用默認日誌記錄器記錄放棄重連
func LogReconnectGiveUp(host string, totalAttempts int, totalDuration time.Duration) {
	GetDefaultLogger().LogReconnectGiveUp(host, totalAttempts, totalDuration)
}

// LogHealthCheckStart 使用默認日誌記錄器記錄健康檢查開始
func LogHealthCheckStart() {
	GetDefaultLogger().LogHealthCheckStart()
}

// LogHealthCheckSuccess 使用默認日誌記錄器記錄健康檢查成功
func LogHealthCheckSuccess(duration time.Duration) {
	GetDefaultLogger().LogHealthCheckSuccess(duration)
}

// LogHealthCheckFailed 使用默認日誌記錄器記錄健康檢查失敗
func LogHealthCheckFailed(err error, duration time.Duration) {
	GetDefaultLogger().LogHealthCheckFailed(err, duration)
}
