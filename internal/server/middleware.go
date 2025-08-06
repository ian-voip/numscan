package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// ValidationMiddleware 自定義驗證中間件
func ValidationMiddleware(ctx huma.Context, next func(huma.Context)) {
	// 添加自定義的請求驗證邏輯
	if ctx.Operation() != nil {
		// 檢查 Content-Type（如果是 POST/PUT 請求）
		method := ctx.Method()
		if method == "POST" || method == "PUT" || method == "PATCH" {
			contentType := ctx.Header("Content-Type")
			if contentType != "" && !strings.Contains(contentType, "application/json") {
				ctx.SetStatus(http.StatusUnsupportedMediaType)
				ctx.BodyWriter().Write([]byte(`{"error": "不支援的媒體類型，請使用 application/json"}`))
				return
			}
		}
	}
	
	next(ctx)
}

// CORSMiddleware CORS 中間件
func CORSMiddleware(ctx huma.Context, next func(huma.Context)) {
	// 設定 CORS headers
	ctx.SetHeader("Access-Control-Allow-Origin", "*")
	ctx.SetHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	ctx.SetHeader("Access-Control-Allow-Headers", "Content-Type, Authorization")
	
	if ctx.Method() == "OPTIONS" {
		ctx.SetStatus(http.StatusNoContent)
		return
	}
	
	next(ctx)
}

// RequestLogMiddleware 請求日誌中間件
func RequestLogMiddleware(ctx huma.Context, next func(huma.Context)) {
	operation := ctx.Operation()
	if operation != nil {
		fmt.Printf("API請求: %s %s [%s]\n", 
			ctx.Method(), 
			ctx.URL().Path, 
			operation.OperationID)
	}
	
	next(ctx)
}

// RateLimitMiddleware 簡單的速率限制中間件示例
func RateLimitMiddleware(ctx huma.Context, next func(huma.Context)) {
	// 這裡可以實現速率限制邏輯
	// 例如：檢查 IP 地址，限制每分鐘請求次數等
	
	next(ctx)
}

// PhoneNumberValidator 電話號碼格式驗證器
func ValidatePhoneNumber(number string) error {
	if len(number) < 8 {
		return fmt.Errorf("電話號碼長度不能少於8位")
	}
	
	if len(number) > 20 {
		return fmt.Errorf("電話號碼長度不能超過20位")
	}
	
	// 檢查是否包含有效字符
	for _, char := range number {
		if !((char >= '0' && char <= '9') || char == '+' || char == '-' || char == ' ' || char == '(' || char == ')') {
			return fmt.Errorf("電話號碼包含無效字符: %c", char)
		}
	}
	
	return nil
}

// BusinessValidationMiddleware 業務邏輯驗證中間件
func BusinessValidationMiddleware(ctx huma.Context, next func(huma.Context)) {
	operation := ctx.Operation()
	if operation == nil {
		next(ctx)
		return
	}
	
	// 針對特定操作進行業務驗證
	switch operation.OperationID {
	case "addNumbers":
		// 可以在這裡添加特定的業務邏輯驗證
		// 例如：檢查用戶權限、配額限制等
		break
	case "createScanJob":
		// 掃描作業的業務驗證
		// 例如：檢查併發限制、資源可用性等
		break
	}
	
	next(ctx)
}

// ErrorHandlingMiddleware 統一錯誤處理中間件
func ErrorHandlingMiddleware(ctx huma.Context, next func(huma.Context)) {
	defer func() {
		if r := recover(); r != nil {
			// 處理 panic 並轉換為適當的 HTTP 錯誤
			ctx.SetStatus(http.StatusInternalServerError)
			ctx.BodyWriter().Write([]byte(fmt.Sprintf(`{"error": "內部伺服器錯誤: %v"}`, r)))
		}
	}()
	
	next(ctx)
}