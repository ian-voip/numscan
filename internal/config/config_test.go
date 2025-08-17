package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	// 測試載入預設配置
	config, err := Load("")
	if err != nil {
		t.Fatalf("Failed to load default config: %v", err)
	}

	// 驗證 ESL 預設值
	if config.ESL.Host != "localhost:8021" {
		t.Errorf("Expected ESL host 'localhost:8021', got '%s'", config.ESL.Host)
	}

	if config.ESL.Password != "ClueCon" {
		t.Errorf("Expected ESL password 'ClueCon', got '%s'", config.ESL.Password)
	}

	if config.ESL.ReconnectInterval != 5*time.Second {
		t.Errorf("Expected ESL reconnect interval 5s, got %v", config.ESL.ReconnectInterval)
	}

	if config.ESL.HealthCheckInterval != 30*time.Second {
		t.Errorf("Expected ESL health check interval 30s, got %v", config.ESL.HealthCheckInterval)
	}

	if config.ESL.MaxReconnectAttempts != 10 {
		t.Errorf("Expected ESL max reconnect attempts 10, got %d", config.ESL.MaxReconnectAttempts)
	}
}

func TestESLConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    ESLConfig
		expectErr bool
	}{
		{
			name: "valid config",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "password",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 10,
			},
			expectErr: false,
		},
		{
			name: "empty host",
			config: ESLConfig{
				Host:                 "",
				Password:             "password",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 10,
			},
			expectErr: true,
		},
		{
			name: "empty password",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 10,
			},
			expectErr: true,
		},
		{
			name: "invalid reconnect interval",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "password",
				ReconnectInterval:    500 * time.Millisecond,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 10,
			},
			expectErr: true,
		},
		{
			name: "invalid health check interval",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "password",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  2 * time.Second,
				MaxReconnectAttempts: 10,
			},
			expectErr: true,
		},
		{
			name: "invalid max reconnect attempts - too low",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "password",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 0,
			},
			expectErr: true,
		},
		{
			name: "invalid max reconnect attempts - too high",
			config: ESLConfig{
				Host:                 "localhost:8021",
				Password:             "password",
				ReconnectInterval:    5 * time.Second,
				HealthCheckInterval:  30 * time.Second,
				MaxReconnectAttempts: 101,
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateESLConfig(&tt.config)
			if tt.expectErr && err == nil {
				t.Errorf("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}

func TestConfigFromEnvironment(t *testing.T) {
	// 設置環境變數 - 使用正確的環境變數名稱格式
	os.Setenv("NUMSCAN_ESL_HOST", "test-host:8021")
	os.Setenv("NUMSCAN_ESL_PASSWORD", "test-password")
	os.Setenv("NUMSCAN_ESL_RECONNECT_INTERVAL", "10s")
	os.Setenv("NUMSCAN_ESL_HEALTH_CHECK_INTERVAL", "60s")
	os.Setenv("NUMSCAN_ESL_MAX_RECONNECT_ATTEMPTS", "20")

	defer func() {
		os.Unsetenv("NUMSCAN_ESL_HOST")
		os.Unsetenv("NUMSCAN_ESL_PASSWORD")
		os.Unsetenv("NUMSCAN_ESL_RECONNECT_INTERVAL")
		os.Unsetenv("NUMSCAN_ESL_HEALTH_CHECK_INTERVAL")
		os.Unsetenv("NUMSCAN_ESL_MAX_RECONNECT_ATTEMPTS")
	}()

	config, err := Load("")
	if err != nil {
		t.Fatalf("Failed to load config from environment: %v", err)
	}

	if config.ESL.Host != "test-host:8021" {
		t.Errorf("Expected ESL host from env 'test-host:8021', got '%s'", config.ESL.Host)
	}

	if config.ESL.Password != "test-password" {
		t.Errorf("Expected ESL password from env 'test-password', got '%s'", config.ESL.Password)
	}

	if config.ESL.ReconnectInterval != 10*time.Second {
		t.Errorf("Expected ESL reconnect interval from env 10s, got %v", config.ESL.ReconnectInterval)
	}

	if config.ESL.HealthCheckInterval != 60*time.Second {
		t.Errorf("Expected ESL health check interval from env 60s, got %v", config.ESL.HealthCheckInterval)
	}

	if config.ESL.MaxReconnectAttempts != 20 {
		t.Errorf("Expected ESL max reconnect attempts from env 20, got %d", config.ESL.MaxReconnectAttempts)
	}
}

func TestConfigValidationIntegration(t *testing.T) {
	// 測試完整的配置驗證流程
	config := &Config{
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     "5432",
			User:     "postgres",
			Password: "password",
			DBName:   "testdb",
			SSLMode:  "disable",
			TimeZone: "UTC",
		},
		App: AppConfig{
			RingTime:    30,
			Concurrency: 10,
			DialDelay:   100,
		},
		ESL: ESLConfig{
			Host:                 "localhost:8021",
			Password:             "ClueCon",
			ReconnectInterval:    5 * time.Second,
			HealthCheckInterval:  30 * time.Second,
			MaxReconnectAttempts: 10,
		},
	}

	err := validateConfig(config)
	if err != nil {
		t.Errorf("Valid config should pass validation, got error: %v", err)
	}

	// 測試無效配置
	config.ESL.Host = ""
	err = validateConfig(config)
	if err == nil {
		t.Error("Invalid config should fail validation")
	}
}
