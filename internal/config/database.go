package config

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Database DatabaseConfig `koanf:"database"`
	App      AppConfig      `koanf:"app"`
	ESL      ESLConfig      `koanf:"esl"`
	Logging  LoggingConfig  `koanf:"logging"`
}

type DatabaseConfig struct {
	Host     string `koanf:"host"`
	Port     string `koanf:"port"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	DBName   string `koanf:"dbname"`
	SSLMode  string `koanf:"sslmode"`
	TimeZone string `koanf:"timezone"`
}

type AppConfig struct {
	RingTime           int `koanf:"ring_time"`
	Concurrency        int `koanf:"concurrency"`
	DialDelay          int `koanf:"dial_delay"`
	ScanTimeoutBuffer  int `koanf:"scan_timeout_buffer"`
	DialTimeoutBuffer  int `koanf:"dial_timeout_buffer"`
	DBOperationTimeout int `koanf:"db_operation_timeout"`
}

type ESLConfig struct {
	Host                 string        `koanf:"host"`
	Password             string        `koanf:"password"`
	ReconnectInterval    time.Duration `koanf:"reconnect_interval"`
	HealthCheckInterval  time.Duration `koanf:"health_check_interval"`
	MaxReconnectAttempts int           `koanf:"max_reconnect_attempts"`
}

type LoggingConfig struct {
	Level      string `koanf:"level"`       // debug, info, warn, error
	Format     string `koanf:"format"`      // json, text
	Output     string `koanf:"output"`      // stdout, stderr, file
	FilePath   string `koanf:"file_path"`   // log file path when output is file
	MaxSize    int    `koanf:"max_size"`    // max size in MB
	MaxBackups int    `koanf:"max_backups"` // max number of backup files
	MaxAge     int    `koanf:"max_age"`     // max age in days
	Compress   bool   `koanf:"compress"`    // compress old log files
}

var GlobalConfig *Config

func Load(configPath string) (*Config, error) {
	k := koanf.New(".")

	if configPath != "" {
		if err := k.Load(file.Provider(configPath), toml.Parser()); err != nil {
			log.Printf("Warning: failed to load config file %s: %v", configPath, err)
		}
	}

	if err := k.Load(env.Provider("NUMSCAN_", ".", func(s string) string {
		return strings.ReplaceAll(strings.ToLower(
			strings.TrimPrefix(s, "NUMSCAN_")), "_", ".")
	}), nil); err != nil {
		return nil, fmt.Errorf("failed to load environment variables: %w", err)
	}

	var config Config
	if err := k.Unmarshal("", &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 處理環境變數中的 duration 字串
	if err := parseDurationFromEnv(&config, k); err != nil {
		return nil, fmt.Errorf("failed to parse duration from environment: %w", err)
	}

	setDefaults(&config)

	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	GlobalConfig = &config

	return &config, nil
}

func setDefaults(cfg *Config) {
	if cfg.Database.Host == "" {
		cfg.Database.Host = "localhost"
	}
	if cfg.Database.Port == "" {
		cfg.Database.Port = "5432"
	}
	if cfg.Database.User == "" {
		cfg.Database.User = "postgres"
	}
	if cfg.Database.DBName == "" {
		cfg.Database.DBName = "numscan"
	}
	if cfg.Database.SSLMode == "" {
		cfg.Database.SSLMode = "disable"
	}
	if cfg.Database.TimeZone == "" {
		cfg.Database.TimeZone = "Asia/Taipei"
	}

	if cfg.App.RingTime == 0 {
		cfg.App.RingTime = 30
	}
	if cfg.App.Concurrency == 0 {
		cfg.App.Concurrency = 1
	}
	if cfg.App.DialDelay == 0 {
		cfg.App.DialDelay = 100
	}
	if cfg.App.ScanTimeoutBuffer == 0 {
		cfg.App.ScanTimeoutBuffer = 60
	}
	if cfg.App.DialTimeoutBuffer == 0 {
		cfg.App.DialTimeoutBuffer = 15
	}
	if cfg.App.DBOperationTimeout == 0 {
		cfg.App.DBOperationTimeout = 30
	}

	// ESL 預設值
	if cfg.ESL.Host == "" {
		cfg.ESL.Host = "localhost:8021"
	}
	if cfg.ESL.Password == "" {
		cfg.ESL.Password = "ClueCon"
	}
	if cfg.ESL.ReconnectInterval == 0 {
		cfg.ESL.ReconnectInterval = 5 * time.Second
	}
	if cfg.ESL.HealthCheckInterval == 0 {
		cfg.ESL.HealthCheckInterval = 30 * time.Second
	}
	if cfg.ESL.MaxReconnectAttempts == 0 {
		cfg.ESL.MaxReconnectAttempts = 10
	}

	// Logging 預設值
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "text"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}
	if cfg.Logging.MaxSize == 0 {
		cfg.Logging.MaxSize = 100 // 100MB
	}
	if cfg.Logging.MaxBackups == 0 {
		cfg.Logging.MaxBackups = 3
	}
	if cfg.Logging.MaxAge == 0 {
		cfg.Logging.MaxAge = 28 // 28 days
	}
}

func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		c.Host, c.User, c.Password, c.DBName, c.Port, c.SSLMode)
}

func (c *DatabaseConfig) PGXDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&timezone=%s",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode, c.TimeZone)
}

func parseDurationFromEnv(cfg *Config, k *koanf.Koanf) error {
	// 解析 ESL 相關的 duration 環境變數
	if reconnectStr := k.String("esl.reconnect.interval"); reconnectStr != "" {
		if duration, err := time.ParseDuration(reconnectStr); err == nil {
			cfg.ESL.ReconnectInterval = duration
		}
	}

	if healthCheckStr := k.String("esl.health.check.interval"); healthCheckStr != "" {
		if duration, err := time.ParseDuration(healthCheckStr); err == nil {
			cfg.ESL.HealthCheckInterval = duration
		}
	}

	// 解析 ESL 相關的整數環境變數
	if maxAttemptsStr := k.String("esl.max.reconnect.attempts"); maxAttemptsStr != "" {
		if attempts, err := strconv.Atoi(maxAttemptsStr); err == nil {
			cfg.ESL.MaxReconnectAttempts = attempts
		}
	}

	return nil
}

func validateConfig(cfg *Config) error {
	// 驗證 ESL 配置
	if err := validateESLConfig(&cfg.ESL); err != nil {
		return fmt.Errorf("ESL config validation failed: %w", err)
	}

	// 驗證 Database 配置
	if err := validateDatabaseConfig(&cfg.Database); err != nil {
		return fmt.Errorf("database config validation failed: %w", err)
	}

	// 驗證 App 配置
	if err := validateAppConfig(&cfg.App); err != nil {
		return fmt.Errorf("app config validation failed: %w", err)
	}

	// 驗證 Logging 配置
	if err := validateLoggingConfig(&cfg.Logging); err != nil {
		return fmt.Errorf("logging config validation failed: %w", err)
	}

	return nil
}

func validateESLConfig(cfg *ESLConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("ESL host cannot be empty")
	}

	if cfg.Password == "" {
		return fmt.Errorf("ESL password cannot be empty")
	}

	if cfg.ReconnectInterval < time.Second {
		return fmt.Errorf("ESL reconnect interval must be at least 1 second, got %v", cfg.ReconnectInterval)
	}

	if cfg.HealthCheckInterval < 5*time.Second {
		return fmt.Errorf("ESL health check interval must be at least 5 seconds, got %v", cfg.HealthCheckInterval)
	}

	if cfg.MaxReconnectAttempts < 1 {
		return fmt.Errorf("ESL max reconnect attempts must be at least 1, got %d", cfg.MaxReconnectAttempts)
	}

	if cfg.MaxReconnectAttempts > 100 {
		return fmt.Errorf("ESL max reconnect attempts cannot exceed 100, got %d", cfg.MaxReconnectAttempts)
	}

	return nil
}

func validateDatabaseConfig(cfg *DatabaseConfig) error {
	if cfg.Host == "" {
		return fmt.Errorf("database host cannot be empty")
	}

	if cfg.Port == "" {
		return fmt.Errorf("database port cannot be empty")
	}

	if cfg.User == "" {
		return fmt.Errorf("database user cannot be empty")
	}

	if cfg.DBName == "" {
		return fmt.Errorf("database name cannot be empty")
	}

	return nil
}

func validateAppConfig(cfg *AppConfig) error {
	if cfg.RingTime < 1 {
		return fmt.Errorf("app ring time must be at least 1 second, got %d", cfg.RingTime)
	}

	if cfg.RingTime > 300 {
		return fmt.Errorf("app ring time cannot exceed 300 seconds, got %d", cfg.RingTime)
	}

	if cfg.Concurrency < 1 {
		return fmt.Errorf("app concurrency must be at least 1, got %d", cfg.Concurrency)
	}

	if cfg.Concurrency > 1000 {
		return fmt.Errorf("app concurrency cannot exceed 1000, got %d", cfg.Concurrency)
	}

	if cfg.DialDelay < 0 {
		return fmt.Errorf("app dial delay cannot be negative, got %d", cfg.DialDelay)
	}

	return nil
}

func validateLoggingConfig(cfg *LoggingConfig) error {
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	if !validLevels[cfg.Level] {
		return fmt.Errorf("invalid logging level: %s, must be one of: debug, info, warn, error", cfg.Level)
	}

	validFormats := map[string]bool{
		"json": true,
		"text": true,
	}

	if !validFormats[cfg.Format] {
		return fmt.Errorf("invalid logging format: %s, must be one of: json, text", cfg.Format)
	}

	validOutputs := map[string]bool{
		"stdout": true,
		"stderr": true,
		"file":   true,
	}

	if !validOutputs[cfg.Output] {
		return fmt.Errorf("invalid logging output: %s, must be one of: stdout, stderr, file", cfg.Output)
	}

	if cfg.Output == "file" && cfg.FilePath == "" {
		return fmt.Errorf("file_path must be specified when output is 'file'")
	}

	if cfg.MaxSize < 1 || cfg.MaxSize > 1000 {
		return fmt.Errorf("max_size must be between 1 and 1000 MB, got %d", cfg.MaxSize)
	}

	if cfg.MaxBackups < 0 || cfg.MaxBackups > 100 {
		return fmt.Errorf("max_backups must be between 0 and 100, got %d", cfg.MaxBackups)
	}

	if cfg.MaxAge < 1 || cfg.MaxAge > 365 {
		return fmt.Errorf("max_age must be between 1 and 365 days, got %d", cfg.MaxAge)
	}

	return nil
}

func GetConfig() *Config {
	return GlobalConfig
}
