package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/percipia/eslgo"
	"gorm.io/gorm"

	"numscan/internal/query"
)

type ScanConfig struct {
	Numbers     []string
	RingTime    int
	Concurrency int
	DialDelay   int
	ESLHost     string
	ESLPassword string
}

type ScanResult struct {
	Number      string    `json:"number"`
	Answered    bool      `json:"answered"`
	HangupCause string    `json:"hangup_cause,omitempty"`
	Details     string    `json:"details,omitempty"`
	ProcessedAt time.Time `json:"processed_at"`
}

type ScanService struct {
	db    *gorm.DB
	query *query.Query
}

func NewScanService(db *gorm.DB, q *query.Query) *ScanService {
	return &ScanService{
		db:    db,
		query: q,
	}
}

func (s *ScanService) Scan(ctx context.Context, config *ScanConfig) ([]ScanResult, error) {
	// 設定預設值
	if config.ESLHost == "" {
		config.ESLHost = "64.52.80.198:8021"
	}
	if config.ESLPassword == "" {
		config.ESLPassword = "tomoon"
	}
	if config.RingTime == 0 {
		config.RingTime = 30
	}
	if config.Concurrency == 0 {
		config.Concurrency = 1
	}
	if config.DialDelay == 0 {
		config.DialDelay = 100
	}

	if len(config.Numbers) == 0 {
		return nil, fmt.Errorf("no phone numbers provided")
	}

	conn, err := eslgo.Dial(config.ESLHost, config.ESLPassword, func() {
		log.Printf("與 FreeSWITCH 中斷連線")
	})
	if err != nil {
		return nil, fmt.Errorf("error connecting to FreeSWITCH: %w", err)
	}
	defer conn.ExitAndClose()

	if err := conn.EnableEvents(context.Background()); err != nil {
		return nil, fmt.Errorf("啟用事件接收失敗: %w", err)
	}

	log.Printf("成功連接到 FreeSWITCH，開始掃描 %d 個號碼", len(config.Numbers))

	var resultsMu sync.RWMutex
	results := make([]ScanResult, 0, len(config.Numbers))
	answeredNumbers := make(map[string]bool)

	var uuidMutex sync.RWMutex
	numberToUUID := make(map[string]string)
	uuidToNumber := make(map[string]string)

	processAnswerFunc := func(number string) {
		uuidMutex.Lock()
		answeredNumbers[number] = true
		uuidMutex.Unlock()
		log.Printf("號碼 %s 已接聽 (狀態已記錄)", number)
	}

	processHangupFunc := func(number, cause string) {
		resultsMu.Lock()
		defer resultsMu.Unlock()

		// 使用 GORM Gen 查詢檢查電話號碼是否已被處理過
		phoneRecord, err := s.query.PhoneNumber.Where(s.query.PhoneNumber.Number.Eq(number)).First()
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Printf("查詢電話號碼 %s 時發生錯誤: %v", number, err)
			return
		}

		// 如果找到記錄且已經處理過，檢查是否應該跳過
		if phoneRecord != nil && phoneRecord.IsProcessed() {
			if cause != "ALLOTTED_TIMEOUT" {
				log.Printf("號碼 %s 已被處理過，且掛斷原因非 ALLOTTED_TIMEOUT，跳過", number)
				return
			}
		}

		uuidMutex.RLock()
		isAnswered := answeredNumbers[number]
		uuidMutex.RUnlock()

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

		results = append(results, result)

		log.Printf("結果已記錄: 號碼=%s, 接聽=%v, 掛斷原因=%s, 詳細=%s",
			number, result.Answered, result.HangupCause, result.Details)
	}

	listenerID := conn.RegisterEventListener(eslgo.EventListenAll, func(event *eslgo.Event) {
		eventName := event.GetName()
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

		number := event.GetHeader("Other-Leg-Destination-Number")
		if number == "" {
			number = event.GetHeader("Caller-Destination-Number")
		}

		switch eventName {
		case "CHANNEL_CREATE":
			log.Printf("通話建立: UUID=%s, 號碼=%s", uuid, number)
			if number != "" && uuid != "" {
				uuidMutex.Lock()
				numberToUUID[number] = uuid
				uuidToNumber[uuid] = number
				uuidMutex.Unlock()
			}
		case "CHANNEL_ANSWER":
			log.Printf("通話接聽: UUID=%s, 號碼=%s", uuid, number)
			if number == "" && uuid != "" {
				uuidMutex.RLock()
				number = uuidToNumber[uuid]
				uuidMutex.RUnlock()
			}
			if number != "" {
				processAnswerFunc(number)
			}
		case "CHANNEL_HANGUP":
			cause := event.GetHeader("Hangup-Cause")
			log.Printf("通話掛斷: UUID=%s, 號碼=%s, Cause=%s", uuid, number, cause)
			if number == "" && uuid != "" {
				uuidMutex.RLock()
				number = uuidToNumber[uuid]
				uuidMutex.RUnlock()
			}
			if number != "" {
				processHangupFunc(number, cause)
			}
		}
	})

	defer conn.RemoveEventListener(eslgo.EventListenAll, listenerID)

	jobs := make(chan dialJob, len(config.Numbers))
	var wg sync.WaitGroup

	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			log.Printf("工作執行器 #%d 啟動", workerID)

			for job := range jobs {
				s.processDialJob(conn, job.index, job.number, job.uuid, config)
				if config.DialDelay > 0 {
					log.Printf("工作執行器 #%d 延遲 %d 毫秒", workerID, config.DialDelay)
					time.Sleep(time.Duration(config.DialDelay) * time.Millisecond)
				}
			}

			log.Printf("工作執行器 #%d 結束", workerID)
		}(i)
	}

	log.Printf("開始派發 %d 個撥號任務到 %d 個工作執行器", len(config.Numbers), config.Concurrency)
	for i, number := range config.Numbers {
		uuid := fmt.Sprintf("service-call-%d", i)
		jobs <- dialJob{
			index:  i,
			number: number,
			uuid:   uuid,
		}
	}
	close(jobs)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	log.Println("等待撥號結果...")

	totalEstimatedTime := time.Duration(len(config.Numbers)/config.Concurrency+1) * time.Duration(config.RingTime) * time.Second
	if config.DialDelay > 0 {
		totalEstimatedTime += time.Duration(len(config.Numbers)*config.DialDelay) * time.Millisecond
	}
	timeoutDuration := totalEstimatedTime + 30*time.Second

	log.Printf("預估總處理時間: %v, 設定超時時間: %v", totalEstimatedTime, timeoutDuration)

	select {
	case <-done:
		log.Println("所有撥號任務已完成，等待額外10秒接收最後的結果...")
		time.Sleep(10 * time.Second)
	case <-time.After(timeoutDuration):
		log.Printf("等待結果逾時（%v），將使用已有的結果", timeoutDuration)
	case <-ctx.Done():
		log.Println("掃描被取消")
		return nil, ctx.Err()
	}

	resultsMu.RLock()
	finalResults := make([]ScanResult, len(results))
	copy(finalResults, results)
	resultsMu.RUnlock()

	log.Printf("檢查是否有未處理的號碼...")
	unprocessedCount := 0
	// 建立已處理號碼的 map 用於快速查找
	processedInResults := make(map[string]bool)
	resultsMu.RLock()
	for _, result := range results {
		processedInResults[result.Number] = true
	}
	resultsMu.RUnlock()

	for _, number := range config.Numbers {
		if !processedInResults[number] {
			unprocessedCount++
			log.Printf("號碼 %s 無結果（第%d個未處理），標記為 TIMEOUT", number, unprocessedCount)

			result := ScanResult{
				Number:      number,
				Answered:    false,
				HangupCause: "TIMEOUT",
				ProcessedAt: time.Now(),
			}

			resultsMu.Lock()
			results = append(results, result)
			resultsMu.Unlock()
		}
	}

	if unprocessedCount > 0 {
		log.Printf("警告：有 %d 個號碼未收到結果，可能需要調整超時時間或檢查網路連接", unprocessedCount)
	}

	resultsMu.RLock()
	finalResults = make([]ScanResult, len(results))
	copy(finalResults, results)
	resultsMu.RUnlock()

	log.Printf("掃描完成！總號碼數: %d, 已處理: %d", len(config.Numbers), len(finalResults))

	return finalResults, nil
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
