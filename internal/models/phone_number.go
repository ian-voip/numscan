package models

import (
	"time"

	"gorm.io/gorm"
)

// ProcessingStatus 處理狀態（流程狀態）
type ProcessingStatus int

const (
	StatusInitial ProcessingStatus = iota
	StatusPending
	StatusProcessing
	StatusDialing
	StatusCompleted
)

// String returns the string representation of ProcessingStatus
func (s ProcessingStatus) String() string {
	switch s {
	case StatusInitial:
		return "initial"
	case StatusPending:
		return "pending"
	case StatusProcessing:
		return "processing"
	case StatusDialing:
		return "dialing"
	case StatusCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// CallResult 撥號結果
type CallResult int

const (
	ResultNone CallResult = iota
	ResultAnswered
	ResultNoAnswer
	ResultBusy
	ResultFailed
	ResultTimeout
)

// String returns the string representation of CallResult
func (r CallResult) String() string {
	switch r {
	case ResultNone:
		return "none"
	case ResultAnswered:
		return "answered"
	case ResultNoAnswer:
		return "no_answer"
	case ResultBusy:
		return "busy"
	case ResultFailed:
		return "failed"
	case ResultTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

type PhoneNumber struct {
	gorm.Model

	// 電話號碼（加上唯一索引避免重複匯入）
	Number string `gorm:"uniqueIndex;not null" json:"number"`

	// 處理狀態（流程狀態）
	Status ProcessingStatus `gorm:"default:0;index" json:"status"`

	// 撥號結果
	Result CallResult `gorm:"default:0;index" json:"result"`

	// 處理時間記錄
	ProcessingStartedAt   *time.Time `json:"processing_started_at,omitempty"`
	ProcessingCompletedAt *time.Time `json:"processing_completed_at,omitempty"`

	// 掛斷原因（FreeSWITCH hangup cause）
	HangupCause string `json:"hangup_cause,omitempty"`

	// 錯誤訊息（如果處理失敗）
	ErrorMessage string `json:"error_message,omitempty"`

	// 重試次數
	RetryCount int `gorm:"default:0" json:"retry_count"`
}

func (PhoneNumber) TableName() string {
	return "phone_numbers"
}

// IsProcessed 檢查電話號碼是否已被處理過
func (p *PhoneNumber) IsProcessed() bool {
	return p.Status == StatusCompleted
}

// CanRetry 檢查是否可以重試
func (p *PhoneNumber) CanRetry(maxRetries int) bool {
	return p.Result == ResultFailed && p.RetryCount < maxRetries
}

// MarkAsProcessing 標記為處理中
func (p *PhoneNumber) MarkAsProcessing() {
	p.Status = StatusProcessing
	now := time.Now()
	p.ProcessingStartedAt = &now
}

// MarkAsDialing 標記為撥號中
func (p *PhoneNumber) MarkAsDialing() {
	p.Status = StatusDialing
	if p.ProcessingStartedAt == nil {
		now := time.Now()
		p.ProcessingStartedAt = &now
	}
}

// MarkAsCompleted 標記為完成並設定結果
func (p *PhoneNumber) MarkAsCompleted(result CallResult, hangupCause string) {
	p.Status = StatusCompleted
	p.Result = result
	p.HangupCause = hangupCause
	now := time.Now()
	p.ProcessingCompletedAt = &now
	p.ErrorMessage = ""
}

// MarkAsFailed 標記為失敗
func (p *PhoneNumber) MarkAsFailed(errorMsg string) {
	p.Result = ResultFailed
	p.ErrorMessage = errorMsg
	p.RetryCount++
	now := time.Now()
	p.ProcessingCompletedAt = &now
}
