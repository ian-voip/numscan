package main

import (
	"log"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gen"
	"gorm.io/gorm"

	"numscan/internal/config"
	"numscan/internal/models"
)

func main() {
	// 清理舊的生成文件
	outPath := "./internal/query"
	log.Println("Cleaning old generated files...")
	if err := os.RemoveAll(outPath); err != nil {
		log.Printf("Warning: failed to remove old files: %v", err)
	}

	cfg, err := config.Load("config.toml")
	if err != nil {
		log.Fatal("Failed to load config:", err)
	}

	db, err := gorm.Open(postgres.Open(cfg.Database.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	g := gen.NewGenerator(gen.Config{
		OutPath:       outPath,
		Mode:          gen.WithoutContext | gen.WithDefaultQuery | gen.WithQueryInterface,
		FieldNullable: true,
	})

	g.UseDB(db)

	g.ApplyBasic(
		&models.PhoneNumber{},
	)

	g.Execute()

	log.Println("Gen code generated successfully!")
}
