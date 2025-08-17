# 配置管理說明

NumScan 使用 [koanf v2](https://github.com/knadh/koanf) 進行配置管理，支援 TOML 文件和環境變數。

## 配置來源優先級

1. **環境變數** (最高優先級)
2. **TOML 配置文件** 
3. **預設值** (最低優先級)

## TOML 配置文件

建立 `config.toml` 文件：

```toml
[database]
host = "localhost"
port = "5432"
user = "postgres"
password = "your_password"
dbname = "numscan"
sslmode = "disable"
timezone = "Asia/Taipei"

[esl]
host = "localhost:8021"                    # FreeSWITCH ESL server host and port
password = "ClueCon"                       # ESL authentication password
reconnect_interval = "5s"                  # Interval between reconnection attempts
health_check_interval = "30s"              # Interval for connection health checks
max_reconnect_attempts = 10                # Maximum number of reconnection attempts

[app]
ring_time = 30      # Ring time in seconds
concurrency = 10    # Number of concurrent dial operations
dial_delay = 100    # Delay between dials in milliseconds
```

## 環境變數

使用 `NUMSCAN_` 前綴，層級用底線分隔：

```bash
# 資料庫配置
export NUMSCAN_DATABASE_HOST=localhost
export NUMSCAN_DATABASE_PORT=5432
export NUMSCAN_DATABASE_USER=postgres
export NUMSCAN_DATABASE_PASSWORD=your_password
export NUMSCAN_DATABASE_DBNAME=numscan
export NUMSCAN_DATABASE_SSLMODE=disable
export NUMSCAN_DATABASE_TIMEZONE=Asia/Taipei

# ESL 配置
export NUMSCAN_ESL_HOST=localhost:8021
export NUMSCAN_ESL_PASSWORD=ClueCon
export NUMSCAN_ESL_RECONNECT_INTERVAL=5s
export NUMSCAN_ESL_HEALTH_CHECK_INTERVAL=30s
export NUMSCAN_ESL_MAX_RECONNECT_ATTEMPTS=10

# 應用程式配置
export NUMSCAN_APP_RING_TIME=30
export NUMSCAN_APP_CONCURRENCY=10
export NUMSCAN_APP_DIAL_DELAY=100
```

## 程式碼中使用配置

```go
import "numscan/internal/config"

// 載入配置（傳入 TOML 文件路徑，可為空字串使用預設值）
cfg, err := config.Load("config.toml")
if err != nil {
    log.Fatal(err)
}

// 或者取得全域配置
cfg := config.GetConfig()

// 使用配置
fmt.Println("Database Host:", cfg.Database.Host)
fmt.Println("Concurrency:", cfg.App.Concurrency)
```

## 預設值

如果未設定配置，將使用以下預設值：

### 資料庫
- Host: `localhost`
- Port: `5432`
- User: `postgres`
- Password: `""` (空字串)
- DBName: `numscan`
- SSLMode: `disable`
- TimeZone: `Asia/Taipei`

### ESL
- Host: `localhost:8021`
- Password: `ClueCon`
- ReconnectInterval: `5s`
- HealthCheckInterval: `30s`
- MaxReconnectAttempts: `10`

### 應用程式
- RingTime: `30` 秒
- Concurrency: `1`
- DialDelay: `100` 毫秒

## 開發環境設定

1. 複製環境變數範例：
   ```bash
   cp .env.example .env
   ```

2. 編輯 `.env` 文件設定你的值

3. 載入環境變數：
   ```bash
   source .env  # 或使用 dotenv 工具
   ```