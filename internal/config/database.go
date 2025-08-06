package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Database DatabaseConfig `koanf:"database"`
	App      AppConfig      `koanf:"app"`
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
	RingTime    int `koanf:"ring_time"`
	Concurrency int `koanf:"concurrency"`
	DialDelay   int `koanf:"dial_delay"`
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
	
	setDefaults(&config)
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
}

func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=%s",
		c.Host, c.User, c.Password, c.DBName, c.Port, c.SSLMode)
}

func (c *DatabaseConfig) PGXDSN() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&timezone=%s",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode, c.TimeZone)
}

func GetConfig() *Config {
	return GlobalConfig
}