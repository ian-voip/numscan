package queue

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/riverqueue/river"
	"gorm.io/gorm"

	"numscan/internal/config"
	"numscan/internal/models"
	"numscan/internal/query"
	"numscan/internal/services"
)

// ScanPhoneNumberArgs 掃描電話號碼的作業參數
type ScanPhoneNumberArgs struct {
	PhoneNumberID uint   `json:"phone_number_id"`
	Number        string `json:"number"`
	RingTime      int    `json:"ring_time"`
	DialDelay     int    `json:"dial_delay"`
}

// Kind 實現 river.JobArgs 介面
func (ScanPhoneNumberArgs) Kind() string { return "scan_phone_number" }

// InsertOpts 實現 river.JobArgsWithInsertOpts 介面，禁用重試機制
func (ScanPhoneNumberArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 1, // 只嘗試一次，不重試
	}
}

// ScanPhoneNumberWorker 掃描電話號碼的 worker
type ScanPhoneNumberWorker struct {
	river.WorkerDefaults[ScanPhoneNumberArgs]
	db          *gorm.DB
	query       *query.Query
	scanService *services.ScanService
}

// NewScanPhoneNumberWorker 建立新的掃描電話號碼 worker
func NewScanPhoneNumberWorker(db *gorm.DB, q *query.Query, scanService *services.ScanService) *ScanPhoneNumberWorker {
	return &ScanPhoneNumberWorker{
		db:          db,
		query:       q,
		scanService: scanService,
	}
}

// Work 執行掃描電話號碼的工作
func (w *ScanPhoneNumberWorker) Work(ctx context.Context, job *river.Job[ScanPhoneNumberArgs]) error {
	args := job.Args
	log.Printf("開始處理電話號碼: %s (ID: %d)", args.Number, args.PhoneNumberID)

	// 更新資料庫狀態為處理中
	phoneNumberDAO := w.query.PhoneNumber
	phoneNumber, err := phoneNumberDAO.WithContext(ctx).
		Where(phoneNumberDAO.ID.Eq(args.PhoneNumberID)).
		First()

	if err != nil {
		return fmt.Errorf("查詢電話號碼失敗: %w", err)
	}

	// 標記為處理中
	phoneNumber.MarkAsProcessing()
	if err := phoneNumberDAO.WithContext(ctx).Save(phoneNumber); err != nil {
		return fmt.Errorf("更新電話號碼狀態失敗: %w", err)
	}

	// 獲取應用配置
	appConfig := config.GetConfig()
	if appConfig == nil {
		return fmt.Errorf("無法獲取應用配置")
	}

	// 創建掃描配置
	scanConfig := &services.ScanConfig{
		Numbers:            []string{args.Number},
		RingTime:           args.RingTime,
		Concurrency:        1, // 單個號碼使用單一併發
		DialDelay:          args.DialDelay,
		ScanTimeoutBuffer:  appConfig.App.ScanTimeoutBuffer,
		DialTimeoutBuffer:  appConfig.App.DialTimeoutBuffer,
		DBOperationTimeout: appConfig.App.DBOperationTimeout,
	}

	// 執行掃描
	results, err := w.scanService.Scan(ctx, scanConfig)

	if err != nil {
		// 標記為失敗
		phoneNumber.MarkAsFailed(err.Error())
		// 使用新的context來避免deadline exceeded錯誤
		saveCtx, cancel := context.WithTimeout(context.Background(), time.Duration(appConfig.App.DBOperationTimeout)*time.Second)
		defer cancel()
		if saveErr := phoneNumberDAO.WithContext(saveCtx).Save(phoneNumber); saveErr != nil {
			log.Printf("儲存失敗狀態時發生錯誤: %v", saveErr)
		}
		return fmt.Errorf("掃描電話號碼失敗: %w", err)
	}

	// 處理掃描結果
	if len(results) > 0 {
		result := results[0] // 只有一個號碼
		var callResult models.CallResult

		if result.Answered {
			callResult = models.ResultAnswered
		} else {
			// 根據 hangup_cause 判斷具體原因
			switch result.HangupCause {
			case "NO_ANSWER":
				callResult = models.ResultNoAnswer
			case "USER_BUSY":
				callResult = models.ResultBusy
			case "CALL_REJECTED":
				callResult = models.ResultFailed
			case "NO_ROUTE_DESTINATION":
				callResult = models.ResultFailed
			default:
				if result.HangupCause != "" {
					callResult = models.ResultNoAnswer
				} else {
					callResult = models.ResultTimeout
				}
			}
		}

		phoneNumber.MarkAsCompleted(callResult, result.HangupCause)
	} else {
		// 沒有結果，標記為失敗
		phoneNumber.MarkAsFailed("沒有得到掃描結果")
	}

	// 儲存最終結果 - 使用新的context來避免deadline exceeded錯誤
	saveCtx, cancel := context.WithTimeout(context.Background(), time.Duration(appConfig.App.DBOperationTimeout)*time.Second)
	defer cancel()
	if err := phoneNumberDAO.WithContext(saveCtx).Save(phoneNumber); err != nil {
		return fmt.Errorf("儲存掃描結果失敗: %w", err)
	}

	log.Printf("完成處理電話號碼: %s (ID: %d)", args.Number, args.PhoneNumberID)
	return nil
}

// BatchScanJobArgs 批次掃描作業的參數
type BatchScanJobArgs struct {
	JobID       string   `json:"job_id"`
	Numbers     []string `json:"numbers"`
	RingTime    int      `json:"ring_time"`
	Concurrency int      `json:"concurrency"`
	DialDelay   int      `json:"dial_delay"`
}

// Kind 實現 river.JobArgs 介面
func (BatchScanJobArgs) Kind() string { return "batch_scan_job" }

// InsertOpts 實現 river.JobArgsWithInsertOpts 介面，禁用重試機制
func (BatchScanJobArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 1, // 只嘗試一次，不重試
	}
}

// BatchScanJobWorker 批次掃描作業的 worker
type BatchScanJobWorker struct {
	river.WorkerDefaults[BatchScanJobArgs]
	riverClient *river.Client[*sql.Tx]
}

// NewBatchScanJobWorker 建立新的批次掃描作業 worker
func NewBatchScanJobWorker(client *river.Client[*sql.Tx]) *BatchScanJobWorker {
	return &BatchScanJobWorker{
		riverClient: client,
	}
}

// Work 執行批次掃描作業
func (w *BatchScanJobWorker) Work(ctx context.Context, job *river.Job[BatchScanJobArgs]) error {
	args := job.Args
	log.Printf("開始批次掃描作業: %s，號碼數量: %d", args.JobID, len(args.Numbers))

	// 為每個號碼創建個別的掃描作業
	var jobs []river.InsertManyParams
	for _, number := range args.Numbers {
		// 這裡我們直接使用掃描配置，而不存儲到資料庫
		// 如果需要存儲，可以先創建 PhoneNumber 記錄
		insertParams := river.InsertManyParams{
			Args: ScanPhoneNumberArgs{
				Number:    number,
				RingTime:  args.RingTime,
				DialDelay: args.DialDelay,
			},
		}
		// 設定延遲執行時間
		delay := time.Duration(len(jobs)*args.DialDelay) * time.Millisecond
		insertParams.InsertOpts = &river.InsertOpts{
			ScheduledAt: time.Now().Add(delay),
		}
		jobs = append(jobs, insertParams)
	}

	// 批次插入作業到佇列
	_, err := w.riverClient.InsertMany(ctx, jobs)
	if err != nil {
		return fmt.Errorf("插入掃描作業到佇列失敗: %w", err)
	}

	log.Printf("成功創建 %d 個掃描作業", len(jobs))
	return nil
}
