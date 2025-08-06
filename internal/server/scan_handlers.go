package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"numscan/internal/services"
)

// ===== 掃描作業 API Input/Output Types =====

// ScanJobInput 創建掃描作業的輸入
type ScanJobInput struct {
	Body struct {
		Numbers     []string `json:"numbers" minItems:"1" maxItems:"10000" doc:"要掃描的電話號碼陣列" example:"[\"+886912345678\"]"`
		RingTime    int      `json:"ring_time" minimum:"5" maximum:"120" default:"30" doc:"響鈴時間（秒）" example:"30"`
		Concurrency int      `json:"concurrency" minimum:"1" maximum:"50" default:"1" doc:"併發數量" example:"5"`
		DialDelay   int      `json:"dial_delay" minimum:"0" maximum:"10000" default:"100" doc:"撥號間隔（毫秒）" example:"100"`
	}
}

// ScanJobOutput 創建掃描作業的輸出
type ScanJobOutput struct {
	Body struct {
		JobID   string `json:"job_id" doc:"作業ID" example:"job_1640995200"`
		Status  string `json:"status" doc:"作業狀態" example:"started"`
		Message string `json:"message" doc:"訊息" example:"掃描作業已開始"`
		Total   int    `json:"total" doc:"總號碼數" example:"100"`
	}
}

// GetScanJobInput 查詢掃描作業的輸入
type GetScanJobInput struct {
	JobID string `path:"jobId" doc:"作業ID" example:"job_1640995200"`
}

// GetScanJobOutput 查詢掃描作業的輸出
type GetScanJobOutput struct {
	Body struct {
		JobID    string               `json:"job_id" doc:"作業ID" example:"job_1640995200"`
		Status   string               `json:"status" doc:"作業狀態" example:"completed"`
		Progress int                  `json:"progress" doc:"已完成數量" example:"95"`
		Total    int                  `json:"total" doc:"總數量" example:"100"`
		Results  []services.ScanResult `json:"results,omitempty" doc:"掃描結果"`
		Error    string               `json:"error,omitempty" doc:"錯誤訊息"`
	}
}

// ScanJob 掃描作業結構
type ScanJob struct {
	ID      string
	Status  string
	Results []services.ScanResult
	Error   error
	Total   int
	mu      sync.RWMutex
}

// 全局作業管理
var (
	jobs   = make(map[string]*ScanJob)
	jobsMu sync.RWMutex
)

// ===== 掃描作業 API Handlers =====

// registerScanAPI 註冊掃描相關的 API
func (s *Server) registerScanAPI() {
	// 創建掃描作業
	huma.Register(s.api, huma.Operation{
		OperationID: "createScanJob",
		Method:      http.MethodPost,
		Path:        "/api/v1/scan",
		Summary:     "創建掃描作業",
		Description: "創建新的電話號碼掃描作業，支援批量撥號檢測",
		Tags:        []string{"scan"},
	}, s.createScanJob)

	// 查詢掃描作業狀態
	huma.Register(s.api, huma.Operation{
		OperationID: "getScanJob",
		Method:      http.MethodGet,
		Path:        "/api/v1/scan/{jobId}",
		Summary:     "查詢掃描作業狀態",
		Description: "根據作業ID查詢掃描作業的執行狀態和結果",
		Tags:        []string{"scan"},
	}, s.getScanJob)
}

// createScanJob 創建掃描作業
func (s *Server) createScanJob(ctx context.Context, input *ScanJobInput) (*ScanJobOutput, error) {
	req := input.Body

	if len(req.Numbers) == 0 {
		return nil, huma.Error400BadRequest("電話號碼列表不能為空")
	}

	// 設定預設值
	ringTime := req.RingTime
	if ringTime == 0 {
		ringTime = 30
	}

	concurrency := req.Concurrency
	if concurrency == 0 {
		concurrency = 1
	}

	dialDelay := req.DialDelay
	if dialDelay == 0 {
		dialDelay = 100
	}

	jobID := generateJobID()

	job := &ScanJob{
		ID:     jobID,
		Status: "running",
		Total:  len(req.Numbers),
	}

	jobsMu.Lock()
	jobs[jobID] = job
	jobsMu.Unlock()

	// 在背景執行掃描
	go func() {
		config := &services.ScanConfig{
			Numbers:     req.Numbers,
			RingTime:    ringTime,
			Concurrency: concurrency,
			DialDelay:   dialDelay,
		}

		results, err := s.scanService.Scan(context.Background(), config)

		job.mu.Lock()
		if err != nil {
			job.Status = "error"
			job.Error = err
		} else {
			job.Status = "completed"
			job.Results = results
		}
		job.mu.Unlock()
	}()

	output := &ScanJobOutput{}
	output.Body.JobID = jobID
	output.Body.Status = "started"
	output.Body.Message = fmt.Sprintf("掃描作業已開始，將處理 %d 個電話號碼", len(req.Numbers))
	output.Body.Total = len(req.Numbers)

	return output, nil
}

// getScanJob 查詢掃描作業狀態
func (s *Server) getScanJob(ctx context.Context, input *GetScanJobInput) (*GetScanJobOutput, error) {
	jobID := input.JobID

	if jobID == "" {
		return nil, huma.Error400BadRequest("作業ID不能為空")
	}

	jobsMu.RLock()
	job, exists := jobs[jobID]
	jobsMu.RUnlock()

	if !exists {
		return nil, huma.Error404NotFound("找不到指定的作業")
	}

	job.mu.RLock()
	defer job.mu.RUnlock()

	progress := len(job.Results)

	output := &GetScanJobOutput{}
	output.Body.JobID = jobID
	output.Body.Status = job.Status
	output.Body.Progress = progress
	output.Body.Total = job.Total
	output.Body.Results = job.Results

	if job.Error != nil {
		output.Body.Error = job.Error.Error()
	}

	return output, nil
}

// generateJobID 生成作業ID
func generateJobID() string {
	return fmt.Sprintf("job_%d", time.Now().Unix())
}