package logger

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name    string
		config  *LoggingConfig
		wantErr bool
	}{
		{
			name: "valid text logger",
			config: &LoggingConfig{
				Level:  "info",
				Format: "text",
				Output: "stdout",
			},
			wantErr: false,
		},
		{
			name: "valid json logger",
			config: &LoggingConfig{
				Level:  "debug",
				Format: "json",
				Output: "stderr",
			},
			wantErr: false,
		},
		{
			name: "invalid log level",
			config: &LoggingConfig{
				Level:  "invalid",
				Format: "text",
				Output: "stdout",
			},
			wantErr: true,
		},
		{
			name: "invalid format",
			config: &LoggingConfig{
				Level:  "info",
				Format: "invalid",
				Output: "stdout",
			},
			wantErr: true,
		},
		{
			name: "invalid output",
			config: &LoggingConfig{
				Level:  "info",
				Format: "text",
				Output: "invalid",
			},
			wantErr: true,
		},
		{
			name:    "nil config uses defaults",
			config:  nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := NewLogger(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewLogger() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && logger == nil {
				t.Errorf("NewLogger() returned nil logger without error")
			}
		})
	}
}

func TestLogESLEvent(t *testing.T) {
	// 創建一個緩衝區來捕獲日誌輸出
	var buf bytes.Buffer

	// 創建一個寫入緩衝區的日誌記錄器
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})

	logger := &Logger{
		Logger: slog.New(handler),
		level:  slog.LevelDebug,
	}

	tests := []struct {
		name     string
		event    ESLEvent
		message  string
		attrs    []slog.Attr
		wantText string
	}{
		{
			name:     "connection success event",
			event:    ESLEventConnectionSuccess,
			message:  "Connection established",
			attrs:    []slog.Attr{slog.String("host", "localhost:8021")},
			wantText: "connection_success",
		},
		{
			name:     "connection failed event",
			event:    ESLEventConnectionFailed,
			message:  "Connection failed",
			attrs:    []slog.Attr{slog.String("error", "timeout")},
			wantText: "connection_failed",
		},
		{
			name:     "health check event",
			event:    ESLEventHealthCheckSuccess,
			message:  "Health check passed",
			attrs:    []slog.Attr{slog.Duration("duration", 100*time.Millisecond)},
			wantText: "health_check_success",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			logger.LogESLEvent(tt.event, tt.message, tt.attrs...)

			output := buf.String()
			if !strings.Contains(output, tt.wantText) {
				t.Errorf("LogESLEvent() output = %v, want to contain %v", output, tt.wantText)
			}
			if !strings.Contains(output, tt.message) {
				t.Errorf("LogESLEvent() output = %v, want to contain message %v", output, tt.message)
			}
		})
	}
}

func TestConnectionMetrics(t *testing.T) {
	// 重置指標
	ResetConnectionMetrics()

	// 測試初始狀態
	metrics := GetConnectionMetrics()
	if metrics.TotalConnections != 0 {
		t.Errorf("Initial TotalConnections = %v, want 0", metrics.TotalConnections)
	}

	// 模擬一些連接事件
	LogConnectionAttempt("localhost:8021", 1)
	LogConnectionSuccess("localhost:8021", 100*time.Millisecond)

	metrics = GetConnectionMetrics()
	if metrics.TotalConnections != 1 {
		t.Errorf("After connection attempt TotalConnections = %v, want 1", metrics.TotalConnections)
	}
	if metrics.SuccessfulConnections != 1 {
		t.Errorf("After connection success SuccessfulConnections = %v, want 1", metrics.SuccessfulConnections)
	}

	// 模擬連接失敗
	LogConnectionFailed("localhost:8021", errors.New("timeout"), 5*time.Second)

	metrics = GetConnectionMetrics()
	if metrics.FailedConnections != 1 {
		t.Errorf("After connection failure FailedConnections = %v, want 1", metrics.FailedConnections)
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		name      string
		levelStr  string
		wantLevel slog.Level
		wantErr   bool
	}{
		{"debug level", "debug", slog.LevelDebug, false},
		{"info level", "info", slog.LevelInfo, false},
		{"warn level", "warn", slog.LevelWarn, false},
		{"warning level", "warning", slog.LevelWarn, false},
		{"error level", "error", slog.LevelError, false},
		{"invalid level", "invalid", slog.LevelInfo, true},
		{"empty level", "", slog.LevelInfo, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLevel, err := parseLogLevel(tt.levelStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLogLevel() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotLevel != tt.wantLevel {
				t.Errorf("parseLogLevel() = %v, want %v", gotLevel, tt.wantLevel)
			}
		})
	}
}

func TestInitDefaultLogger(t *testing.T) {
	// 測試初始化默認日誌記錄器
	config := &LoggingConfig{
		Level:  "info",
		Format: "text",
		Output: "stdout",
	}

	err := InitDefaultLogger(config)
	if err != nil {
		t.Errorf("InitDefaultLogger() error = %v", err)
	}

	logger := GetDefaultLogger()
	if logger == nil {
		t.Errorf("GetDefaultLogger() returned nil")
	}

	// 測試使用默認日誌記錄器
	LogConnectionAttempt("test:8021", 1)
	LogHealthCheckStart()
	LogHealthCheckSuccess(50 * time.Millisecond)
}

func BenchmarkLogESLEvent(b *testing.B) {
	config := &LoggingConfig{
		Level:  "info",
		Format: "json",
		Output: "stdout",
	}

	logger, err := NewLogger(config)
	if err != nil {
		b.Fatalf("Failed to create logger: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.LogESLEvent(ESLEventConnectionSuccess, "Test message",
			slog.String("host", "localhost:8021"),
			slog.Int("attempt", i),
		)
	}
}
