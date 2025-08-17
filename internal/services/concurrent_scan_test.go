package services

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentScanContextManagement 測試並發掃描上下文管理
func TestConcurrentScanContextManagement(t *testing.T) {
	scanService := NewScanService(nil, nil, nil)

	// 測試創建掃描上下文
	ctx := context.Background()
	numbers := []string{"1001", "1002", "1003"}
	scanCtx := scanService.createScanContext("test-scan-1", numbers, ctx)

	if scanCtx == nil {
		t.Fatal("Expected scan context to be created")
	}

	if scanCtx.scanID != "test-scan-1" {
		t.Errorf("Expected scanID to be 'test-scan-1', got %s", scanCtx.scanID)
	}

	if len(scanCtx.numbers) != 3 {
		t.Errorf("Expected 3 numbers, got %d", len(scanCtx.numbers))
	}

	// 測試註冊掃描上下文
	err := scanService.registerScanContext(scanCtx)
	if err != nil {
		t.Errorf("Expected no error registering scan context, got: %v", err)
	}

	// 測試根據號碼查找掃描上下文
	foundCtx := scanService.getScanContextByNumber("1002")
	if foundCtx == nil {
		t.Error("Expected to find scan context by number")
	}

	if foundCtx.scanID != "test-scan-1" {
		t.Errorf("Expected found context scanID to be 'test-scan-1', got %s", foundCtx.scanID)
	}

	// 測試UUID映射
	scanCtx.uuidMu.Lock()
	scanCtx.numberToUUID["1001"] = "uuid-1001"
	scanCtx.uuidToNumber["uuid-1001"] = "1001"
	scanCtx.uuidMu.Unlock()

	foundCtxByUUID := scanService.getScanContextByUUID("uuid-1001")
	if foundCtxByUUID == nil {
		t.Error("Expected to find scan context by UUID")
	}

	if foundCtxByUUID.scanID != "test-scan-1" {
		t.Errorf("Expected found context scanID to be 'test-scan-1', got %s", foundCtxByUUID.scanID)
	}

	// 測試移除掃描上下文
	scanService.unregisterScanContext("test-scan-1")

	foundCtxAfterRemoval := scanService.getScanContextByNumber("1002")
	if foundCtxAfterRemoval != nil {
		t.Error("Expected scan context to be removed")
	}
}

// TestEventHandlerMethods 測試事件處理方法
func TestEventHandlerMethods(t *testing.T) {
	scanService := NewScanService(nil, nil, nil)

	// 創建掃描上下文
	ctx := context.Background()
	scanCtx := scanService.createScanContext("scan-1", []string{"2001", "2002"}, ctx)
	scanService.registerScanContext(scanCtx)

	// 測試 handleChannelCreate
	scanService.handleChannelCreate(scanCtx, "uuid-2001", "2001")

	scanCtx.uuidMu.RLock()
	uuid := scanCtx.numberToUUID["2001"]
	number := scanCtx.uuidToNumber["uuid-2001"]
	scanCtx.uuidMu.RUnlock()

	if uuid != "uuid-2001" {
		t.Errorf("Expected UUID mapping for 2001 to be 'uuid-2001', got %s", uuid)
	}

	if number != "2001" {
		t.Errorf("Expected number mapping for 'uuid-2001' to be '2001', got %s", number)
	}

	// 測試 handleChannelAnswer
	scanService.handleChannelAnswer(scanCtx, "uuid-2001", "")

	scanCtx.uuidMu.RLock()
	answered := scanCtx.answeredNumbers["2001"]
	scanCtx.uuidMu.RUnlock()

	if !answered {
		t.Error("Expected 2001 to be marked as answered")
	}

	// 清理
	scanService.unregisterScanContext("scan-1")
}

// TestConcurrentScanContextIsolation 測試並發掃描上下文隔離
func TestConcurrentScanContextIsolation(t *testing.T) {
	scanService := NewScanService(nil, nil, nil)

	ctx := context.Background()

	// 創建多個掃描上下文
	scanCtx1 := scanService.createScanContext("scan-1", []string{"4001", "4002"}, ctx)
	scanCtx2 := scanService.createScanContext("scan-2", []string{"5001", "5002"}, ctx)
	scanCtx3 := scanService.createScanContext("scan-3", []string{"6001", "6002"}, ctx)

	scanService.registerScanContext(scanCtx1)
	scanService.registerScanContext(scanCtx2)
	scanService.registerScanContext(scanCtx3)

	// 並發處理事件
	var wg sync.WaitGroup

	// 為 scan-1 處理事件
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanService.handleChannelCreate(scanCtx1, "scan-1-call-0", "4001")
		scanService.handleChannelAnswer(scanCtx1, "scan-1-call-0", "")
	}()

	// 為 scan-2 處理事件
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanService.handleChannelCreate(scanCtx2, "scan-2-call-0", "5001")
	}()

	wg.Wait()

	// 驗證事件隔離
	// scan-1 應該有UUID映射和接聽記錄
	scanCtx1.uuidMu.RLock()
	answered1 := scanCtx1.answeredNumbers["4001"]
	uuid1 := scanCtx1.numberToUUID["4001"]
	scanCtx1.uuidMu.RUnlock()

	if !answered1 {
		t.Error("Expected 4001 to be marked as answered in scan-1")
	}

	if uuid1 != "scan-1-call-0" {
		t.Errorf("Expected UUID mapping for 4001 in scan-1, got %s", uuid1)
	}

	// scan-2 應該只有UUID映射，沒有接聽記錄
	scanCtx2.uuidMu.RLock()
	answered2 := scanCtx2.answeredNumbers["5001"]
	uuid2 := scanCtx2.numberToUUID["5001"]
	scanCtx2.uuidMu.RUnlock()

	if answered2 {
		t.Error("Expected 5001 to NOT be marked as answered in scan-2")
	}

	if uuid2 != "scan-2-call-0" {
		t.Errorf("Expected UUID mapping for 5001 in scan-2, got %s", uuid2)
	}

	// scan-3 應該沒有任何記錄
	scanCtx3.uuidMu.RLock()
	hasAnyMapping := len(scanCtx3.numberToUUID) > 0
	scanCtx3.uuidMu.RUnlock()

	if hasAnyMapping {
		t.Error("Expected scan-3 to have no UUID mappings")
	}

	// 清理
	scanService.unregisterScanContext("scan-1")
	scanService.unregisterScanContext("scan-2")
	scanService.unregisterScanContext("scan-3")
}

// TestThreadSafetyOfScanContexts 測試掃描上下文的線程安全性
func TestThreadSafetyOfScanContexts(t *testing.T) {
	scanService := NewScanService(nil, nil, nil)
	ctx := context.Background()

	// 創建掃描上下文
	scanCtx := scanService.createScanContext("thread-safety-test", []string{"7001", "7002", "7003"}, ctx)
	scanService.registerScanContext(scanCtx)

	// 並發讀寫測試
	var wg sync.WaitGroup
	numGoroutines := 10

	// 並發寫入UUID映射
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			uuid := fmt.Sprintf("uuid-%d", id)
			number := fmt.Sprintf("700%d", id%3+1)

			scanCtx.uuidMu.Lock()
			scanCtx.numberToUUID[number] = uuid
			scanCtx.uuidToNumber[uuid] = number
			scanCtx.uuidMu.Unlock()
		}(i)
	}

	// 並發讀取UUID映射
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			number := fmt.Sprintf("700%d", id%3+1)

			scanCtx.uuidMu.RLock()
			_ = scanCtx.numberToUUID[number]
			scanCtx.uuidMu.RUnlock()
		}(i)
	}

	// 並發標記接聽狀態
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			number := fmt.Sprintf("700%d", id%3+1)

			scanCtx.uuidMu.Lock()
			scanCtx.answeredNumbers[number] = true
			scanCtx.uuidMu.Unlock()
		}(i)
	}

	wg.Wait()

	// 驗證沒有發生競爭條件（測試應該能正常完成）
	scanCtx.uuidMu.RLock()
	mappingCount := len(scanCtx.numberToUUID)
	answeredCount := len(scanCtx.answeredNumbers)
	scanCtx.uuidMu.RUnlock()

	if mappingCount == 0 {
		t.Error("Expected some UUID mappings to be created")
	}

	if answeredCount == 0 {
		t.Error("Expected some numbers to be marked as answered")
	}

	// 清理
	scanService.unregisterScanContext("thread-safety-test")
}
