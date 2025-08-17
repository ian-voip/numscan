package services

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/percipia/eslgo"
	"gorm.io/gorm"

	"numscan/internal/esl"
	"numscan/internal/query"
)

type ScanConfig struct {
	Numbers            []string
	RingTime           int
	Concurrency        int
	DialDelay          int
	ScanTimeoutBuffer  int
	DialTimeoutBuffer  int
	DBOperationTimeout int
}

type ScanResult struct {
	Number      string    `json:"number"`
	Answered    bool      `json:"answered"`
	HangupCause string    `json:"hangup_cause,omitempty"`
	Details     string    `json:"details,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
}

// ScanContext 管理單次掃描任務的上下文，支持並發掃描
type ScanContext struct {
	scanID          string             // 唯一掃描任務ID
	numbers         []string           // 要掃描的號碼列表
	results         []ScanResult       // 掃描結果
	resultsMu       sync.RWMutex       // 結果保護鎖
	answeredNumbers map[string]bool    // 已接聽號碼記錄
	numberToUUID    map[string]string  // 號碼到UUID的映射
	uuidToNumber    map[string]string  // UUID到號碼的映射
	uuidMu          sync.RWMutex       // UUID映射保護鎖
	eventListenerID string             // 事件監聽器ID
	done            chan struct{}      // 完成信號
	ctx             context.Context    // 上下文
	cancel          context.CancelFunc // 取消函數
}

type ScanService struct {
	db         *gorm.DB
	query      *query.Query
	eslManager esl.ESLManager

	// 並發掃描管理
	activeScansMu sync.RWMutex
	activeScans   map[string]*ScanContext // scanID -> ScanContext
}

func NewScanService(db *gorm.DB, q *query.Query, eslManager esl.ESLManager) *ScanService {
	return &ScanService{
		db:          db,
		query:       q,
		eslManager:  eslManager,
		activeScans: make(map[string]*ScanContext),
	}
}

// createScanContext 創建新的掃描上下文
func (s *ScanService) createScanContext(scanID string, numbers []string, parentCtx context.Context) *ScanContext {
	ctx, cancel := context.WithCancel(parentCtx)

	return &ScanContext{
		scanID:          scanID,
		numbers:         numbers,
		results:         make([]ScanResult, 0, len(numbers)),
		answeredNumbers: make(map[string]bool),
		numberToUUID:    make(map[string]string),
		uuidToNumber:    make(map[string]string),
		done:            make(chan struct{}),
		ctx:             ctx,
		cancel:          cancel,
	}
}

// registerScanContext 註冊掃描上下文到活躍掃描列表
func (s *ScanService) registerScanContext(scanCtx *ScanContext) error {
	s.activeScansMu.Lock()
	defer s.activeScansMu.Unlock()

	s.activeScans[scanCtx.scanID] = scanCtx
	return nil
}

// unregisterScanContext 從活躍掃描列表中移除掃描上下文
func (s *ScanService) unregisterScanContext(scanID string) {
	s.activeScansMu.Lock()
	defer s.activeScansMu.Unlock()

	if scanCtx, exists := s.activeScans[scanID]; exists {
		scanCtx.cancel()
		delete(s.activeScans, scanID)
	}
}

// getScanContextByUUID 根據UUID查找對應的掃描上下文
func (s *ScanService) getScanContextByUUID(uuid string) *ScanContext {
	s.activeScansMu.RLock()
	defer s.activeScansMu.RUnlock()

	for _, scanCtx := range s.activeScans {
		scanCtx.uuidMu.RLock()
		_, exists := scanCtx.uuidToNumber[uuid]
		scanCtx.uuidMu.RUnlock()

		if exists {
			return scanCtx
		}
	}
	return nil
}

// getScanContextByNumber 根據號碼查找對應的掃描上下文
func (s *ScanService) getScanContextByNumber(number string) *ScanContext {
	s.activeScansMu.RLock()
	defer s.activeScansMu.RUnlock()

	for _, scanCtx := range s.activeScans {
		if slices.Contains(scanCtx.numbers, number) {
			return scanCtx
		}
	}
	return nil
}

func (s *ScanService) Scan(ctx context.Context, config *ScanConfig) ([]ScanResult, error) {
	// 設定預設值
	if config.RingTime == 0 {
		config.RingTime = 30
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.DialDelay == 0 {
		config.DialDelay = 100
	}
	if config.ScanTimeoutBuffer == 0 {
		config.ScanTimeoutBuffer = 60
	}
	if config.DialTimeoutBuffer == 0 {
		config.DialTimeoutBuffer = 15
	}
	if config.DBOperationTimeout == 0 {
		config.DBOperationTimeout = 30
	}

	if len(config.Numbers) == 0 {
		return nil, fmt.Errorf("no phone numbers provided")
	}

	// 使用共享的ESL連接
	conn, err := s.eslManager.GetConnection()
	if err != nil {
		return nil, fmt.Errorf("error getting ESL connection: %w", err)
	}

	// 生成唯一的掃描ID
	scanID := fmt.Sprintf("scan-%d", time.Now().UnixNano())
	log.Printf("[%s] 使用共享ESL連接，開始掃描 %d 個號碼", scanID, len(config.Numbers))

	// 創建掃描上下文
	scanCtx := s.createScanContext(scanID, config.Numbers, ctx)

	// 註冊掃描上下文
	if err := s.registerScanContext(scanCtx); err != nil {
		return nil, fmt.Errorf("failed to register scan context: %w", err)
	}

	// 確保在函數結束時清理掃描上下文
	defer s.unregisterScanContext(scanID)

	// 註冊全局事件監聽器（如果還沒有註冊的話）
	if err := s.ensureGlobalEventListener(); err != nil {
		return nil, fmt.Errorf("failed to register global event listener: %w", err)
	}

	// 執行並發撥號
	if err := s.executeConcurrentDial(scanCtx, conn, config); err != nil {
		return nil, fmt.Errorf("failed to execute concurrent dial: %w", err)
	}

	// 等待結果
	results, err := s.waitForScanResults(scanCtx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for scan results: %w", err)
	}

	log.Printf("[%s] 掃描完成！總號碼數: %d, 已處理: %d", scanID, len(config.Numbers), len(results))
	return results, nil
}

type dialJob struct {
	index  int
	number string
	uuid   string
}

func (s *ScanService) processDialJob(conn *eslgo.Conn, index int, number, uuid string, config *ScanConfig) {
	log.Printf("開始處理撥號任務 #%d: %s (UUID: %s)", index, number, uuid)

	aLeg := eslgo.Leg{CallURL: "null"}
	bLeg := eslgo.Leg{CallURL: fmt.Sprintf("%s XML numscan", number)}
	vars := map[string]string{
		"origination_uuid":             uuid,
		"ignore_early_media":           "true",
		"origination_caller_id_name":   "CLI",
		"origination_caller_id_number": "1000",
		"call_timeout":                 fmt.Sprintf("%d", config.RingTime),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.RingTime+5)*time.Second)
	defer cancel()

	_, err := conn.OriginateCall(ctx, true, aLeg, bLeg, vars)
	if err != nil {
		log.Printf("撥號失敗 #%d: %s, 錯誤: %v", index, number, err)
	} else {
		log.Printf("撥號成功 #%d: %s", index, number)
	}

	log.Printf("撥號任務 #%d 處理完成", index)
}

// globalEventHandler 全局事件處理器，將事件路由到正確的掃描上下文
func (s *ScanService) globalEventHandler(event *eslgo.Event) {
	if event == nil {
		return
	}

	eventName := event.GetName()

	// 提取UUID和號碼信息
	uuid := s.extractUUIDFromEvent(event)
	number := s.extractNumberFromEvent(event)

	// 根據事件類型和可用信息查找對應的掃描上下文
	var scanCtx *ScanContext

	if uuid != "" {
		scanCtx = s.getScanContextByUUID(uuid)
	}

	if scanCtx == nil && number != "" {
		scanCtx = s.getScanContextByNumber(number)
	}

	// 如果找不到對應的掃描上下文，忽略此事件
	if scanCtx == nil {
		return
	}

	// 將事件路由到對應的掃描上下文處理
	s.handleEventForScanContext(scanCtx, event, eventName, uuid, number)
}

// extractUUIDFromEvent 從事件中提取UUID
func (s *ScanService) extractUUIDFromEvent(event *eslgo.Event) string {
	uuid := event.GetHeader("variable_origination_uuid")
	if uuid == "" {
		uuid = event.GetHeader("variable_uuid")
	}
	if uuid == "" {
		uuid = event.GetHeader("variable_call_uuid")
	}
	if uuid == "" {
		uuid = event.GetHeader("Channel-Call-Uuid")
	}
	return uuid
}

// extractNumberFromEvent 從事件中提取號碼
func (s *ScanService) extractNumberFromEvent(event *eslgo.Event) string {
	number := event.GetHeader("Other-Leg-Destination-Number")
	if number == "" {
		number = event.GetHeader("Caller-Destination-Number")
	}
	return number
}

// handleEventForScanContext 為特定掃描上下文處理事件
func (s *ScanService) handleEventForScanContext(scanCtx *ScanContext, event *eslgo.Event, eventName, uuid, number string) {
	switch eventName {
	case "CHANNEL_CREATE":
		s.handleChannelCreate(scanCtx, uuid, number)
	case "CHANNEL_ANSWER":
		s.handleChannelAnswer(scanCtx, uuid, number)
	case "CHANNEL_HANGUP":
		cause := event.GetHeader("Hangup-Cause")
		s.handleChannelHangup(scanCtx, uuid, number, cause)
	}
}

// handleChannelCreate 處理通話建立事件
func (s *ScanService) handleChannelCreate(scanCtx *ScanContext, uuid, number string) {
	if number == "" || uuid == "" {
		return
	}

	// 檢查號碼是否在當前掃描任務中
	if !slices.Contains(scanCtx.numbers, number) {
		return
	}

	log.Printf("[%s] 通話建立: UUID=%s, 號碼=%s", scanCtx.scanID, uuid, number)

	scanCtx.uuidMu.Lock()
	scanCtx.numberToUUID[number] = uuid
	scanCtx.uuidToNumber[uuid] = number
	scanCtx.uuidMu.Unlock()
}

// handleChannelAnswer 處理通話接聽事件
func (s *ScanService) handleChannelAnswer(scanCtx *ScanContext, uuid, number string) {
	// 如果沒有號碼信息，嘗試從UUID映射中獲取
	if number == "" && uuid != "" {
		scanCtx.uuidMu.RLock()
		number = scanCtx.uuidToNumber[uuid]
		scanCtx.uuidMu.RUnlock()
	}

	if number == "" {
		return
	}

	log.Printf("[%s] 通話接聽: UUID=%s, 號碼=%s", scanCtx.scanID, uuid, number)

	scanCtx.uuidMu.Lock()
	scanCtx.answeredNumbers[number] = true
	scanCtx.uuidMu.Unlock()
}

// handleChannelHangup 處理通話掛斷事件
func (s *ScanService) handleChannelHangup(scanCtx *ScanContext, uuid, number, cause string) {
	// 如果沒有號碼信息，嘗試從UUID映射中獲取
	if number == "" && uuid != "" {
		scanCtx.uuidMu.RLock()
		number = scanCtx.uuidToNumber[uuid]
		scanCtx.uuidMu.RUnlock()
	}

	if number == "" {
		return
	}

	log.Printf("[%s] 通話掛斷: UUID=%s, 號碼=%s, Cause=%s", scanCtx.scanID, uuid, number, cause)

	// 處理掛斷結果
	s.processHangupForScanContext(scanCtx, number, cause)
}

// processHangupForScanContext 為特定掃描上下文處理掛斷結果
func (s *ScanService) processHangupForScanContext(scanCtx *ScanContext, number, cause string) {
	scanCtx.resultsMu.Lock()
	defer scanCtx.resultsMu.Unlock()

	// 檢查是否已經在當前掃描上下文中處理過此號碼
	for _, result := range scanCtx.results {
		if result.Number == number {
			log.Printf("[%s] 號碼 %s 在當前掃描中已被處理過，跳過重複處理", scanCtx.scanID, number)
			return
		}
	}

	// 使用 GORM Gen 查詢檢查電話號碼是否已被處理過
	phoneRecord, err := s.query.PhoneNumber.Where(s.query.PhoneNumber.Number.Eq(number)).First()
	if err != nil && err != gorm.ErrRecordNotFound {
		log.Printf("[%s] 查詢電話號碼 %s 時發生錯誤: %v", scanCtx.scanID, number, err)
		return
	}

	// 如果找到記錄且已經處理過，檢查是否應該跳過
	if phoneRecord != nil && phoneRecord.IsProcessed() {
		if cause != "ALLOTTED_TIMEOUT" {
			log.Printf("[%s] 號碼 %s 已被處理過，且掛斷原因非 ALLOTTED_TIMEOUT，跳過", scanCtx.scanID, number)
			return
		}
	}

	scanCtx.uuidMu.RLock()
	isAnswered := scanCtx.answeredNumbers[number]
	scanCtx.uuidMu.RUnlock()

	var result ScanResult
	result.Number = number
	result.ProcessedAt = time.Now()

	if cause == "ALLOTTED_TIMEOUT" {
		result.Answered = true
		result.HangupCause = "ANSWERED"
		result.Details = "ALLOTTED_TIMEOUT"
	} else if isAnswered {
		result.Answered = true
		result.HangupCause = "ANSWERED"
		result.Details = cause
	} else {
		result.Answered = false
		result.HangupCause = cause
	}

	scanCtx.results = append(scanCtx.results, result)

	log.Printf("[%s] 結果已記錄: 號碼=%s, 接聽=%v, 掛斷原因=%s, 詳細=%s",
		scanCtx.scanID, number, result.Answered, result.HangupCause, result.Details)
}

// ensureGlobalEventListener 確保全局事件監聽器已註冊
func (s *ScanService) ensureGlobalEventListener() error {
	// 使用一個簡單的標記來避免重複註冊
	// 在實際應用中，可能需要更複雜的邏輯來管理監聽器
	s.activeScansMu.Lock()
	defer s.activeScansMu.Unlock()

	// 如果已經有活躍的掃描，說明監聽器已經註冊了
	if len(s.activeScans) > 1 {
		return nil
	}

	// 註冊全局事件監聽器
	_, err := s.eslManager.RegisterEventListener(eslgo.EventListenAll, s.globalEventHandler)
	return err
}

// executeConcurrentDial 執行並發撥號
func (s *ScanService) executeConcurrentDial(scanCtx *ScanContext, conn *eslgo.Conn, config *ScanConfig) error {
	jobs := make(chan dialJob, len(config.Numbers))
	var wg sync.WaitGroup

	// 啟動工作執行器
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("[%s] 工作執行器 #%d 啟動", scanCtx.scanID, workerID)

			for job := range jobs {
				s.processDialJobForScanContext(scanCtx, conn, job.index, job.number, job.uuid, config)
				if config.DialDelay > 0 {
					log.Printf("[%s] 工作執行器 #%d 延遲 %d 毫秒", scanCtx.scanID, workerID, config.DialDelay)
					time.Sleep(time.Duration(config.DialDelay) * time.Millisecond)
				}
			}

			log.Printf("[%s] 工作執行器 #%d 結束", scanCtx.scanID, workerID)
		}(i)
	}

	// 派發撥號任務
	log.Printf("[%s] 開始派發 %d 個撥號任務到 %d 個工作執行器", scanCtx.scanID, len(config.Numbers), config.Concurrency)
	for i, number := range config.Numbers {
		uuid := fmt.Sprintf("%s-call-%d", scanCtx.scanID, i)
		jobs <- dialJob{
			index:  i,
			number: number,
			uuid:   uuid,
		}
	}
	close(jobs)

	// 等待所有撥號任務完成
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("[%s] 所有撥號任務已完成", scanCtx.scanID)
	case <-scanCtx.ctx.Done():
		log.Printf("[%s] 撥號任務被取消", scanCtx.scanID)
		return scanCtx.ctx.Err()
	}

	return nil
}

// waitForScanResults 等待掃描結果
func (s *ScanService) waitForScanResults(scanCtx *ScanContext, config *ScanConfig) ([]ScanResult, error) {
	log.Printf("[%s] 等待撥號結果...", scanCtx.scanID)

	// 計算超時時間 - 更合理的計算方式
	batchTime := time.Duration((len(config.Numbers)+config.Concurrency-1)/config.Concurrency) * time.Duration(config.RingTime) * time.Second
	if config.DialDelay > 0 {
		totalDelayTime := time.Duration(len(config.Numbers)*config.DialDelay) * time.Millisecond
		batchTime += totalDelayTime
	}
	// 使用配置的緩衝時間，並且為每個號碼增加5秒緩衝
	timeoutDuration := batchTime + time.Duration(config.ScanTimeoutBuffer+len(config.Numbers)*5)*time.Second

	log.Printf("[%s] 預估批次處理時間: %v, 設定超時時間: %v", scanCtx.scanID, batchTime, timeoutDuration)

	// 等待結果或超時
	select {
	case <-time.After(timeoutDuration):
		log.Printf("[%s] 等待結果逾時（%v），將使用已有的結果", scanCtx.scanID, timeoutDuration)
	case <-scanCtx.ctx.Done():
		log.Printf("[%s] 掃描被取消，原因: %v", scanCtx.scanID, scanCtx.ctx.Err())
		// 不直接返回錯誤，而是使用已有的結果並記錄警告
		log.Printf("[%s] 掃描被取消，但將嘗試返回已有的結果", scanCtx.scanID)
	}

	// 額外等待時間以接收最後的結果
	log.Printf("[%s] 等待額外10秒接收最後的結果...", scanCtx.scanID)
	time.Sleep(10 * time.Second)

	// 收集結果
	scanCtx.resultsMu.RLock()
	results := make([]ScanResult, len(scanCtx.results))
	copy(results, scanCtx.results)
	scanCtx.resultsMu.RUnlock()

	// 檢查未處理的號碼
	s.handleUnprocessedNumbers(scanCtx, results)

	// 返回最終結果
	scanCtx.resultsMu.RLock()
	finalResults := make([]ScanResult, len(scanCtx.results))
	copy(finalResults, scanCtx.results)
	scanCtx.resultsMu.RUnlock()

	return finalResults, nil
}

// handleUnprocessedNumbers 處理未收到結果的號碼
func (s *ScanService) handleUnprocessedNumbers(scanCtx *ScanContext, currentResults []ScanResult) {
	log.Printf("[%s] 檢查是否有未處理的號碼...", scanCtx.scanID)

	// 建立已處理號碼的 map 用於快速查找
	processedInResults := make(map[string]bool)
	for _, result := range currentResults {
		processedInResults[result.Number] = true
	}

	unprocessedCount := 0
	for _, number := range scanCtx.numbers {
		if !processedInResults[number] {
			unprocessedCount++
			log.Printf("[%s] 號碼 %s 無結果（第%d個未處理），標記為 TIMEOUT", scanCtx.scanID, number, unprocessedCount)

			result := ScanResult{
				Number:      number,
				Answered:    false,
				HangupCause: "TIMEOUT",
				ProcessedAt: time.Now(),
			}

			scanCtx.resultsMu.Lock()
			scanCtx.results = append(scanCtx.results, result)
			scanCtx.resultsMu.Unlock()
		}
	}

	if unprocessedCount > 0 {
		log.Printf("[%s] 警告：有 %d 個號碼未收到結果，可能需要調整超時時間或檢查網路連接", scanCtx.scanID, unprocessedCount)
	}
}

// processDialJobForScanContext 為特定掃描上下文處理撥號任務
func (s *ScanService) processDialJobForScanContext(scanCtx *ScanContext, conn *eslgo.Conn, index int, number, uuid string, config *ScanConfig) {
	log.Printf("[%s] 開始處理撥號任務 #%d: %s (UUID: %s)", scanCtx.scanID, index, number, uuid)

	aLeg := eslgo.Leg{CallURL: "null"}
	bLeg := eslgo.Leg{CallURL: fmt.Sprintf("%s XML numscan", number)}
	vars := map[string]string{
		"origination_uuid":             uuid,
		"ignore_early_media":           "true",
		"origination_caller_id_name":   "CLI",
		"origination_caller_id_number": "1000",
		"call_timeout":                 fmt.Sprintf("%d", config.RingTime),
	}

	// 使用配置的緩衝時間來避免context timeout
	ctx, cancel := context.WithTimeout(scanCtx.ctx, time.Duration(config.RingTime+config.DialTimeoutBuffer)*time.Second)
	defer cancel()

	_, err := conn.OriginateCall(ctx, true, aLeg, bLeg, vars)
	if err != nil {
		// 詳細記錄錯誤類型
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("[%s] 撥號任務 #%d: %s 因context timeout失敗: %v", scanCtx.scanID, index, number, err)
		} else {
			log.Printf("[%s] 撥號失敗 #%d: %s, 錯誤: %v", scanCtx.scanID, index, number, err)
		}
	} else {
		log.Printf("[%s] 撥號成功 #%d: %s", scanCtx.scanID, index, number)
	}

	log.Printf("[%s] 撥號任務 #%d 處理完成", scanCtx.scanID, index)
}
