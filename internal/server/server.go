package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"gorm.io/gorm"
	"riverqueue.com/riverui"

	"numscan/internal/config"
	"numscan/internal/database"
	"numscan/internal/esl"
	"numscan/internal/logger"
	"numscan/internal/query"
	"numscan/internal/queue"
	"numscan/internal/services"
)

type Server struct {
	router        chi.Router
	server        *http.Server
	api           huma.API
	db            *gorm.DB
	sqlDB         *sql.DB
	pgxPool       *pgxpool.Pool
	query         *query.Query
	scanService   *services.ScanService  // 共用的掃描服務
	eslManager    *esl.ConnectionManager // ESL連接管理器
	riverClient   *river.Client[*sql.Tx] // 給GORM用的River client
	riverUIClient *river.Client[pgx.Tx]  // 給UI用的River client
	riverUIServer *riverui.Server
}

func New() *Server {
	// 載入配置
	cfg, err := config.Load("config.toml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化結構化日誌系統
	loggerConfig := &logger.LoggingConfig{
		Level:      cfg.Logging.Level,
		Format:     cfg.Logging.Format,
		Output:     cfg.Logging.Output,
		FilePath:   cfg.Logging.FilePath,
		MaxSize:    cfg.Logging.MaxSize,
		MaxBackups: cfg.Logging.MaxBackups,
		MaxAge:     cfg.Logging.MaxAge,
		Compress:   cfg.Logging.Compress,
	}

	if err := logger.InitDefaultLogger(loggerConfig); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	structuredLogger := logger.GetDefaultLogger()
	structuredLogger.Info("NumScan server starting up",
		slog.String("version", "1.0.0"),
		slog.String("log_level", cfg.Logging.Level),
		slog.String("log_format", cfg.Logging.Format),
	)

	// 初始化資料庫連線 (GORM 和 River 共享同一個 sql.DB)
	db, sqlDB, err := database.Initialize("config.toml")
	if err != nil {
		structuredLogger.Error("Failed to initialize database", slog.String("error", err.Error()))
		log.Fatalf("Failed to initialize database: %v", err)
	}
	structuredLogger.Info("Database connection established")

	// 初始化 GORM Gen Query
	q := query.Use(db)

	// 初始化ESL管理器
	eslConfig := &esl.ESLConfig{
		Host:                 cfg.ESL.Host,
		Password:             cfg.ESL.Password,
		ReconnectInterval:    cfg.ESL.ReconnectInterval,
		HealthCheckInterval:  cfg.ESL.HealthCheckInterval,
		MaxReconnectAttempts: cfg.ESL.MaxReconnectAttempts,
	}
	eslManager := esl.NewConnectionManager(eslConfig)
	structuredLogger.Info("ESL manager initialized",
		slog.String("host", cfg.ESL.Host),
		slog.Duration("reconnect_interval", cfg.ESL.ReconnectInterval),
		slog.Duration("health_check_interval", cfg.ESL.HealthCheckInterval),
		slog.Int("max_reconnect_attempts", cfg.ESL.MaxReconnectAttempts),
	)

	// 初始化共用的掃描服務（無狀態服務，可以安全共用）
	scanService := services.NewScanService(db, q, eslManager)

	// 創建 River workers
	workers := river.NewWorkers()
	river.AddWorker(workers, queue.NewScanPhoneNumberWorker(db, q, scanService))

	// 初始化 River client (用於GORM整合) - 使用 sql.DB
	riverClient, err := river.NewClient(riverdatabasesql.New(sqlDB), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 10},
		},
		Workers: workers,
	})
	if err != nil {
		log.Fatalf("Failed to initialize River client: %v", err)
	}

	// 為 River UI 創建獨立的 pgx 連接池和客戶端
	pgxPool, err := pgxpool.New(context.Background(), cfg.Database.PGXDSN())
	if err != nil {
		log.Fatalf("Failed to create pgx pool for River UI: %v", err)
	}

	// 創建獨立的UI workers (可以是空的，因為實際工作由上面的riverClient處理)
	uiWorkers := river.NewWorkers()
	river.AddWorker(uiWorkers, queue.NewScanPhoneNumberWorker(db, q, scanService))

	// 初始化 River UI client - 使用 pgx
	riverUIClient, err := river.NewClient(riverpgxv5.New(pgxPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 1}, // UI客戶端需要至少1個worker
		},
		Workers: uiWorkers,
	})
	if err != nil {
		log.Fatalf("Failed to initialize River UI client: %v", err)
	}

	// 初始化 River UI
	riverUIServer, err := riverui.NewServer(&riverui.ServerOpts{
		Client: riverUIClient,
		DB:     pgxPool,
		Logger: slog.Default(), // 使用結構化 logger
		Prefix: "/admin/river", // 將 UI 掛載到 /admin/river 路徑下
	})
	if err != nil {
		log.Fatalf("Failed to initialize River UI server: %v", err)
	}

	// 初始化 Chi router
	r := chi.NewRouter()

	// 添加基本中間件
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// 初始化 Huma API 配置
	config := huma.DefaultConfig("NumScan API", "1.0.0")
	config.Servers = []*huma.Server{
		{URL: "http://localhost:8080", Description: "Local development server"},
	}

	// 添加全局安全配置（如需要）
	// config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
	// 	"bearerAuth": {
	// 		Type:   "http",
	// 		Scheme: "bearer",
	// 	},
	// }

	api := humachi.New(r, config)

	// 註冊全局中間件
	api.UseMiddleware(
		ErrorHandlingMiddleware,      // 錯誤處理（最外層）
		CORSMiddleware,               // CORS 處理
		RequestLogMiddleware,         // 請求日誌
		ValidationMiddleware,         // 基本驗證
		BusinessValidationMiddleware, // 業務驗證
		// RateLimitMiddleware,    // 速率限制（如需要）
	)

	s := &Server{
		router:        r,
		api:           api,
		db:            db,
		sqlDB:         sqlDB,
		pgxPool:       pgxPool,
		query:         q,
		scanService:   scanService,
		eslManager:    eslManager,
		riverClient:   riverClient,
		riverUIClient: riverUIClient,
		riverUIServer: riverUIServer,
	}

	// 註冊所有 API endpoints
	s.registerAPIRoutes()

	// 註冊 River UI 路由
	s.registerRiverUIRoutes()

	return s
}

func (s *Server) registerAPIRoutes() {
	// 電話號碼管理 API
	s.registerNumbersAPI()

	// 掃描作業 API
	s.registerScanAPI()
}

// registerRiverUIRoutes 註冊 River UI 路由
func (s *Server) registerRiverUIRoutes() {
	// 創建受保護的 River UI 路由群組
	s.router.Route("/admin/river", func(r chi.Router) {
		// 添加基本的權限檢查中間件
		r.Use(s.adminAuthMiddleware)

		// 掛載 River UI
		r.Mount("/", s.riverUIServer)
	})

	log.Printf("River UI mounted at: /admin/river (with authentication)")
}

// adminAuthMiddleware 簡單的管理員認證中間件
func (s *Server) adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 簡單的Basic Auth檢查 (在生產環境中應該使用更安全的認證方式)
		// 檢查是否有 Authorization header 或允許本地訪問

		// 允許本地訪問 (開發模式)
		if r.RemoteAddr == "127.0.0.1" || r.Header.Get("X-Forwarded-For") == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 檢查 Basic Auth (用戶名: admin, 密碼: river)
		username, password, ok := r.BasicAuth()
		if ok && username == "admin" && password == "river" {
			next.ServeHTTP(w, r)
			return
		}

		// 未授權訪問
		w.Header().Set("WWW-Authenticate", `Basic realm="River UI Admin"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

func (s *Server) Start(addr string) error {
	ctx := context.Background()
	structuredLogger := logger.GetDefaultLogger()

	// 啟動ESL連接
	structuredLogger.Info("Connecting to FreeSWITCH ESL server")
	if err := s.eslManager.Connect(ctx); err != nil {
		structuredLogger.Error("Failed to connect to FreeSWITCH", slog.String("error", err.Error()))
		return fmt.Errorf("failed to connect to FreeSWITCH: %w", err)
	}
	structuredLogger.Info("ESL connection established successfully")

	// 啟動主要的 River client（處理實際任務）
	err := s.riverClient.Start(ctx)
	if err != nil {
		structuredLogger.Error("Failed to start River queue worker", slog.String("error", err.Error()))
		return err
	}
	structuredLogger.Info("River queue worker started")

	// 啟動 River UI client（用於監控）
	err = s.riverUIClient.Start(ctx)
	if err != nil {
		structuredLogger.Error("Failed to start River UI client", slog.String("error", err.Error()))
		return err
	}
	structuredLogger.Info("River UI client started")

	// 啟動 River UI server
	err = s.riverUIServer.Start(ctx)
	if err != nil {
		structuredLogger.Error("Failed to start River UI server", slog.String("error", err.Error()))
		return err
	}
	structuredLogger.Info("River UI server started")

	structuredLogger.Info("Starting NumScan API server",
		slog.String("address", addr),
		slog.String("docs_url", fmt.Sprintf("http://%s/docs", addr)),
		slog.String("openapi_url", fmt.Sprintf("http://%s/openapi.json", addr)),
		slog.String("river_ui_url", fmt.Sprintf("http://%s/admin/river", addr)),
	)

	s.server = &http.Server{
		Addr:    addr,
		Handler: s.router,
		// 添加合理的超時設定
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s.server.ListenAndServe()
}

func (s *Server) Stop(ctx context.Context) error {
	var err error
	structuredLogger := logger.GetDefaultLogger()

	structuredLogger.Info("Shutting down NumScan server")

	// 優雅關閉ESL連接
	if s.eslManager != nil {
		structuredLogger.Info("Disconnecting ESL connection")
		if eslErr := s.eslManager.Disconnect(); eslErr != nil {
			structuredLogger.Error("Error disconnecting ESL", slog.String("error", eslErr.Error()))
			if err == nil {
				err = eslErr
			}
		} else {
			structuredLogger.Info("ESL connection closed successfully")
		}
	}

	// 停止主要的 River client
	if s.riverClient != nil {
		structuredLogger.Info("Stopping River queue worker")
		s.riverClient.Stop(ctx)
		structuredLogger.Info("River queue worker stopped")
	}

	// 停止 River UI client
	if s.riverUIClient != nil {
		structuredLogger.Info("Stopping River UI client")
		s.riverUIClient.Stop(ctx)
		structuredLogger.Info("River UI client stopped")
	}

	// 關閉 pgx 連接池
	if s.pgxPool != nil {
		structuredLogger.Info("Closing PGX connection pool")
		s.pgxPool.Close()
		structuredLogger.Info("PGX connection pool closed")
	}

	// River UI server 會隨著 HTTP server 停止而停止
	if s.riverUIServer != nil {
		structuredLogger.Info("River UI server stopped")
	}

	// 停止 HTTP server
	if s.server != nil {
		structuredLogger.Info("Shutting down HTTP server")
		err = s.server.Shutdown(ctx)
		structuredLogger.Info("HTTP server stopped")
	}

	// 記錄連接監控指標
	metrics := logger.GetConnectionMetrics()
	structuredLogger.Info("Final connection metrics",
		slog.Int64("total_connections", metrics.TotalConnections),
		slog.Int64("successful_connections", metrics.SuccessfulConnections),
		slog.Int64("failed_connections", metrics.FailedConnections),
		slog.Int64("total_reconnects", metrics.TotalReconnects),
		slog.Int64("total_health_checks", metrics.TotalHealthChecks),
		slog.Duration("current_uptime", metrics.CurrentUptime),
	)

	structuredLogger.Info("NumScan server shutdown complete")
	return err
}
