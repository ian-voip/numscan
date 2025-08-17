# Design Document

## Overview

本設計將現有的每次掃描任務都建立新ESL連接的架構，改為在server啟動時建立持久ESL連接，並在所有掃描工作中重複使用該連接。設計重點包括連接生命週期管理、線程安全、錯誤處理和自動重連機制。

## Architecture

### 高層架構變更

```
原有架構:
Server -> ScanService -> 每次掃描建立新ESL連接 -> FreeSWITCH

新架構:
Server -> ESLManager (持久連接) -> FreeSWITCH
       -> ScanService -> 使用ESLManager的連接
```

### 核心組件

1. **ESLManager**: 負責ESL連接的生命週期管理
2. **ConnectionPool**: 管理連接狀態和健康檢查
3. **EventDispatcher**: 處理FreeSWITCH事件的分發
4. **修改後的ScanService**: 使用共享的ESL連接

## Components and Interfaces

### ESLManager Interface

```go
type ESLManager interface {
    // 連接管理
    Connect(ctx context.Context) error
    Disconnect() error
    IsConnected() bool
    
    // 連接獲取
    GetConnection() (*eslgo.Conn, error)
    
    // 健康檢查
    HealthCheck(ctx context.Context) error
    
    // 事件監聽
    RegisterEventListener(eventType string, handler EventHandler) (string, error)
    RemoveEventListener(eventType string, listenerID string) error
}

type EventHandler func(event *eslgo.Event)
```

### ESLConfig Structure

```go
type ESLConfig struct {
    Host                string        `toml:"host"`
    Password            string        `toml:"password"`
    ReconnectInterval   time.Duration `toml:"reconnect_interval"`
    HealthCheckInterval time.Duration `toml:"health_check_interval"`
    MaxReconnectAttempts int          `toml:"max_reconnect_attempts"`
}
```

### ConnectionManager Implementation

```go
type ConnectionManager struct {
    config     *ESLConfig
    conn       *eslgo.Conn
    mu         sync.RWMutex
    connected  bool
    
    // 事件處理
    eventListeners map[string]map[string]EventHandler
    eventMu        sync.RWMutex
    
    // 重連控制
    reconnectCtx    context.Context
    reconnectCancel context.CancelFunc
    
    // 健康檢查
    healthCheckTicker *time.Ticker
    healthCheckDone   chan struct{}
}
```

## Data Models

### 配置擴展

在現有的config結構中添加ESL配置：

```go
type Config struct {
    // 現有配置...
    ESL ESLConfig `toml:"esl"`
}
```

### 連接狀態追蹤

```go
type ConnectionStatus struct {
    Connected     bool      `json:"connected"`
    LastConnected time.Time `json:"last_connected"`
    LastError     string    `json:"last_error,omitempty"`
    ReconnectCount int      `json:"reconnect_count"`
}
```

## Error Handling

### 錯誤類型定義

```go
var (
    ErrESLNotConnected    = errors.New("ESL connection not established")
    ErrESLConnectionLost  = errors.New("ESL connection lost")
    ErrESLReconnectFailed = errors.New("ESL reconnection failed")
    ErrESLConfigInvalid   = errors.New("ESL configuration invalid")
)
```

### 錯誤處理策略

1. **連接失敗**: 記錄錯誤，啟動重連機制
2. **連接中斷**: 自動重連，暫停新的掃描任務
3. **重連失敗**: 指數退避重試，達到最大次數後報警
4. **配置錯誤**: 立即失敗，不啟動服務

### 重連機制

```go
func (cm *ConnectionManager) reconnectLoop() {
    backoff := time.Second
    maxBackoff := 30 * time.Second
    attempts := 0
    
    for {
        select {
        case <-cm.reconnectCtx.Done():
            return
        case <-time.After(backoff):
            if err := cm.connect(); err != nil {
                attempts++
                if attempts >= cm.config.MaxReconnectAttempts {
                    log.Printf("Max reconnect attempts reached: %d", attempts)
                    return
                }
                
                // 指數退避
                backoff = min(backoff*2, maxBackoff)
                continue
            }
            
            // 重連成功，重置計數器
            attempts = 0
            backoff = time.Second
            return
        }
    }
}
```

## Testing Strategy

### 單元測試

1. **ESLManager測試**
   - 連接建立和斷開
   - 事件監聽器註冊和移除
   - 健康檢查機制
   - 重連邏輯

2. **ScanService集成測試**
   - 使用mock ESLManager進行測試
   - 驗證掃描邏輯不受連接管理影響
   - 測試並發掃描場景

### 集成測試

1. **FreeSWITCH集成測試**
   - 真實ESL連接測試
   - 事件處理驗證
   - 連接中斷和恢復測試

2. **並發測試**
   - 多個掃描任務同時執行
   - 連接共享的線程安全性
   - 事件分發的正確性

### 性能測試

1. **連接復用效益測試**
   - 對比建立新連接vs復用連接的性能
   - 測量連接建立的開銷

2. **並發性能測試**
   - 高並發掃描場景下的性能表現
   - 內存使用和CPU使用率監控

## Implementation Details

### Server啟動流程修改

```go
func (s *Server) Start(addr string) error {
    // 1. 啟動ESL連接管理器
    if err := s.eslManager.Connect(context.Background()); err != nil {
        return fmt.Errorf("failed to connect to FreeSWITCH: %w", err)
    }
    
    // 2. 啟動其他服務...
    // 3. 啟動HTTP服務器
}
```

### ScanService修改

移除每次掃描時的連接建立邏輯，改為使用ESLManager提供的連接：

```go
func (s *ScanService) Scan(ctx context.Context, config *ScanConfig) ([]ScanResult, error) {
    // 獲取共享連接
    conn, err := s.eslManager.GetConnection()
    if err != nil {
        return nil, fmt.Errorf("failed to get ESL connection: %w", err)
    }
    
    // 使用連接進行掃描...
}
```

### 事件處理優化

實現事件分發機制，允許多個掃描任務註冊不同的事件處理器：

```go
type EventDispatcher struct {
    handlers map[string][]EventHandler
    mu       sync.RWMutex
}

func (ed *EventDispatcher) DispatchEvent(event *eslgo.Event) {
    ed.mu.RLock()
    defer ed.mu.RUnlock()
    
    eventName := event.GetName()
    if handlers, exists := ed.handlers[eventName]; exists {
        for _, handler := range handlers {
            go handler(event) // 異步處理事件
        }
    }
}
```

## Migration Strategy

### 階段性實施

1. **階段1**: 實現ESLManager和基礎連接管理
2. **階段2**: 修改ScanService使用共享連接
3. **階段3**: 實現事件分發和並發優化
4. **階段4**: 添加監控和健康檢查

### 向後兼容性

- 保持現有API接口不變
- 配置文件向後兼容，新增ESL配置項
- 掃描結果格式保持一致

### 部署考慮

- 需要更新配置文件添加ESL設置
- 建議在低峰期部署以測試連接穩定性
- 提供回滾機制以防出現問題