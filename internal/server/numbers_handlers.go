package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"numscan/internal/models"
	"numscan/internal/queue"
)

// ===== 電話號碼管理 API Input/Output Types =====

// AddNumbersInput 添加電話號碼的輸入
type AddNumbersInput struct {
	Body struct {
		Numbers []string `json:"numbers" minItems:"1" maxItems:"1000" doc:"要添加的電話號碼陣列" example:"[\"07743368505\"]"`
	}
}

// AddNumbersOutput 添加電話號碼的輸出
type AddNumbersOutput struct {
	Body struct {
		Success     int      `json:"success" doc:"成功添加的號碼數量" example:"8"`
		Duplicates  int      `json:"duplicates" doc:"重複的號碼數量" example:"2"`
		Invalid     int      `json:"invalid" doc:"無效的號碼數量" example:"1"`
		Total       int      `json:"total" doc:"總共處理的號碼數量" example:"11"`
		Message     string   `json:"message" doc:"處理結果訊息" example:"Successfully processed 11 numbers"`
		InvalidList []string `json:"invalid_list,omitempty" doc:"無效號碼清單" example:"[\"123\"]"`
	}
}

// GetNumbersInput 查詢電話號碼的輸入
type GetNumbersInput struct {
	Status string `query:"status" enum:"initial,pending,processing,dialing,completed" doc:"過濾狀態" example:"pending"`
	Page   int    `query:"page" minimum:"1" default:"1" doc:"頁數，從1開始" example:"1"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" default:"20" doc:"每頁數量，最大100" example:"20"`
}

// GetNumbersOutput 查詢電話號碼的輸出
type GetNumbersOutput struct {
	Body struct {
		Numbers []models.PhoneNumber `json:"numbers" doc:"電話號碼清單"`
		Total   int                  `json:"total" doc:"總數量" example:"150"`
		Page    int                  `json:"page" doc:"當前頁數" example:"1"`
		Limit   int                  `json:"limit" doc:"每頁數量" example:"20"`
	}
}

// ===== 電話號碼管理 API Handlers =====

// registerNumbersAPI 註冊電話號碼管理相關的 API
func (s *Server) registerNumbersAPI() {
	// 添加電話號碼
	huma.Register(s.api, huma.Operation{
		OperationID: "addNumbers",
		Method:      http.MethodPost,
		Path:        "/api/v1/numbers",
		Summary:     "添加電話號碼到待撥清單",
		Description: "批量添加電話號碼到資料庫的待撥清單中，支援重複檢測和基本格式驗證",
		Tags:        []string{"numbers"},
	}, s.addNumbers)

	// 查詢電話號碼
	huma.Register(s.api, huma.Operation{
		OperationID: "getNumbers",
		Method:      http.MethodGet,
		Path:        "/api/v1/numbers",
		Summary:     "查詢電話號碼清單",
		Description: "分頁查詢資料庫中的電話號碼清單，支援按狀態過濾",
		Tags:        []string{"numbers"},
	}, s.getNumbers)
}

// addNumbers 添加電話號碼到待撥清單並加入佇列
func (s *Server) addNumbers(ctx context.Context, input *AddNumbersInput) (*AddNumbersOutput, error) {
	numbers := input.Body.Numbers

	if len(numbers) == 0 {
		return nil, huma.Error400BadRequest("電話號碼列表不能為空")
	}

	var successCount, duplicateCount, invalidCount int
	var invalidList []string
	var createdNumbers []models.PhoneNumber

	phoneNumberDAO := s.query.PhoneNumber

	for _, number := range numbers {
		// 使用統一的號碼驗證器
		if err := ValidatePhoneNumber(number); err != nil {
			invalidCount++
			invalidList = append(invalidList, number)
			continue
		}

		// 檢查是否已存在
		existing, err := phoneNumberDAO.WithContext(ctx).
			Where(phoneNumberDAO.Number.Eq(number)).
			First()

		if err == nil && existing != nil {
			duplicateCount++
			continue
		}

		// 創建新記錄
		phoneNumber := &models.PhoneNumber{
			Number: number,
			Status: models.StatusPending,
		}

		if err := phoneNumberDAO.WithContext(ctx).Create(phoneNumber); err != nil {
			// 創建失敗，可能是併發情況下的重複
			duplicateCount++
			continue
		}

		createdNumbers = append(createdNumbers, *phoneNumber)
		successCount++
	}

	// 批量插入佇列作業
	for _, phoneNumber := range createdNumbers {
		_, err := s.riverClient.Insert(ctx, queue.ScanPhoneNumberArgs{
			PhoneNumberID: phoneNumber.ID,
			Number:        phoneNumber.Number,
			RingTime:      30,  // 預設響鈴時間
			DialDelay:     100, // 預設撥號延遲
		}, nil)

		if err != nil {
			log.Printf("Warning: Failed to queue scan job for number %s: %v", phoneNumber.Number, err)
		}
	}

	err := error(nil)

	if err != nil {
		return nil, huma.Error500InternalServerError("處理電話號碼時發生錯誤", err)
	}

	output := &AddNumbersOutput{}
	output.Body.Success = successCount
	output.Body.Duplicates = duplicateCount
	output.Body.Invalid = invalidCount
	output.Body.Total = len(numbers)
	output.Body.Message = fmt.Sprintf("成功處理 %d 個號碼：新增 %d 個，重複 %d 個，無效 %d 個（已自動加入掃描佇列）",
		len(numbers), successCount, duplicateCount, invalidCount)
	output.Body.InvalidList = invalidList

	return output, nil
}

// getNumbers 查詢電話號碼清單
func (s *Server) getNumbers(ctx context.Context, input *GetNumbersInput) (*GetNumbersOutput, error) {
	// 設定預設值
	page := input.Page
	if page < 1 {
		page = 1
	}

	limit := input.Limit
	if limit < 1 || limit > 100 {
		limit = 20
	}

	phoneNumberDAO := s.query.PhoneNumber
	query := phoneNumberDAO.WithContext(ctx)

	// 根據狀態過濾
	if input.Status != "" {
		switch input.Status {
		case "initial":
			query = query.Where(phoneNumberDAO.Status.Eq(int(models.StatusInitial)))
		case "pending":
			query = query.Where(phoneNumberDAO.Status.Eq(int(models.StatusPending)))
		case "processing":
			query = query.Where(phoneNumberDAO.Status.Eq(int(models.StatusProcessing)))
		case "dialing":
			query = query.Where(phoneNumberDAO.Status.Eq(int(models.StatusDialing)))
		case "completed":
			query = query.Where(phoneNumberDAO.Status.Eq(int(models.StatusCompleted)))
		default:
			return nil, huma.Error400BadRequest("無效的狀態值")
		}
	}

	// 計算總數
	total, err := query.Count()
	if err != nil {
		return nil, huma.Error500InternalServerError("計算號碼總數失敗", err)
	}

	// 分頁查詢
	offset := (page - 1) * limit
	numbers, err := query.
		Order(phoneNumberDAO.CreatedAt.Desc()).
		Offset(offset).
		Limit(limit).
		Find()

	if err != nil {
		return nil, huma.Error500InternalServerError("查詢電話號碼失敗", err)
	}

	// 轉換指針陣列為值陣列
	phoneNumbers := make([]models.PhoneNumber, len(numbers))
	for i, num := range numbers {
		phoneNumbers[i] = *num
	}

	output := &GetNumbersOutput{}
	output.Body.Numbers = phoneNumbers
	output.Body.Total = int(total)
	output.Body.Page = page
	output.Body.Limit = limit

	return output, nil
}
