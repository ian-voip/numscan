package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/lib/pq"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"github.com/jackc/pgx/v5/pgxpool"

	"numscan/internal/config"
	"numscan/internal/models"
)

var (
	DB    *gorm.DB
	SqlDB *sql.DB
)

func Initialize(configPath string) (*gorm.DB, *sql.DB, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load config: %w", err)
	}

	// 直接使用 DSN 創建 GORM 連接
	db, err := gorm.Open(postgres.Open(cfg.Database.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// 獲取底層的 sql.DB
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	DB = db
	SqlDB = sqlDB

	err = AutoMigrate(db)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to auto migrate: %w", err)
	}

	// 執行 River 資料表遷移
	err = migrateRiver(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to migrate river tables: %w", err)
	}

	log.Printf("Database connected successfully - Host: %s, DB: %s",
		cfg.Database.Host, cfg.Database.DBName)
	log.Println("Database migration completed successfully")
	return db, sqlDB, nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&models.PhoneNumber{},
	)
}

func migrateRiver(cfg *config.Config) error {
	// 為 River 遷移創建獨立的 pgx 連接池
	pgxPool, err := pgxpool.New(context.Background(), cfg.Database.PGXDSN())
	if err != nil {
		return fmt.Errorf("failed to create pgx pool for river migration: %w", err)
	}
	defer pgxPool.Close()

	// 執行 River 資料表遷移
	migrator, err := rivermigrate.New(riverpgxv5.New(pgxPool), nil)
	if err != nil {
		return fmt.Errorf("failed to create river migrator: %w", err)
	}
	
	_, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("failed to run river migrations: %w", err)
	}

	log.Println("River migration completed successfully")
	return nil
}

func GetDB() *gorm.DB {
	return DB
}
