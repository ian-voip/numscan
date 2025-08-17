# Implementation Plan

- [x] 1. 創建ESL管理器的核心接口和結構
  - 定義ESLManager接口，包含連接管理、健康檢查和事件監聽方法
  - 創建ConnectionManager結構體，實現線程安全的連接管理
  - 定義ESL配置結構和錯誤類型
  - _Requirements: 1.1, 1.2, 5.1_

- [x] 2. 實現ESL連接管理核心功能
  - 實現Connect方法，建立到FreeSWITCH的ESL連接
  - 實現Disconnect方法，優雅關閉ESL連接
  - 實現IsConnected和GetConnection方法，提供連接狀態查詢
  - 添加線程安全的讀寫鎖保護
  - _Requirements: 1.1, 1.3, 5.1_

- [x] 3. 實現自動重連機制
  - 創建reconnectLoop方法，實現指數退避重連策略
  - 實現連接中斷檢測和自動重連觸發
  - 添加最大重連次數限制和錯誤記錄
  - 實現重連狀態的線程安全管理
  - _Requirements: 1.4, 3.2, 3.3_

- [x] 4. 實現健康檢查機制
  - 創建定期健康檢查功能，監控ESL連接狀態
  - 實現HealthCheck方法，驗證連接可用性
  - 添加健康檢查失敗時的自動重連觸發
  - 實現健康檢查的可配置間隔時間
  - _Requirements: 3.1, 3.2, 3.4_

- [x] 5. 實現事件監聽和分發機制
  - 創建EventDispatcher結構，管理多個事件監聽器
  - 實現RegisterEventListener和RemoveEventListener方法
  - 實現事件分發邏輯，支持多個監聽器同時處理事件
  - 確保事件處理的線程安全性
  - _Requirements: 5.2, 5.3_

- [x] 6. 擴展配置系統支持ESL設置
  - 在config結構中添加ESL配置項
  - 實現ESL配置的加載和驗證
  - 添加預設值和配置驗證邏輯
  - 更新配置文件範例
  - _Requirements: 1.1, 1.3_

- [x] 7. 集成ESLManager到Server結構
  - 在Server結構中添加ESLManager字段
  - 修改Server的New函數，初始化ESLManager
  - 在Server.Start方法中啟動ESL連接
  - 在Server.Stop方法中優雅關閉ESL連接
  - _Requirements: 1.1, 1.4_

- [x] 8. 修改ScanService使用共享ESL連接
  - 移除ScanService中的ESL連接建立邏輯
  - 修改Scan方法使用ESLManager提供的連接
  - 更新事件監聽器註冊，使用ESLManager的事件分發
  - 確保掃描邏輯的線程安全性
  - _Requirements: 2.1, 2.2, 4.1_

- [x] 9. 實現並發掃描的事件處理優化
  - 修改事件處理邏輯，支持多個掃描任務並發執行
  - 實現事件與特定掃描任務的關聯機制
  - 優化UUID和號碼的映射管理，確保線程安全
  - 測試並發掃描場景下的事件正確分發
  - _Requirements: 2.3, 4.3, 5.3, 5.4_

- [ ] 10. 添加錯誤處理和日誌記錄
  - 實現詳細的錯誤處理，包含連接失敗、重連失敗等場景
  - 添加結構化日誌記錄，追蹤連接狀態變化
  - 實現錯誤恢復機制，確保服務穩定性
  - 添加連接狀態的監控指標
  - _Requirements: 1.3, 3.4, 4.4_

- [ ] 11. 創建ESLManager的單元測試
  - 編寫連接管理功能的單元測試
  - 測試重連機制和健康檢查功能
  - 測試事件監聽器的註冊和移除
  - 測試線程安全性和並發場景
  - _Requirements: 5.1, 5.2, 5.3, 5.4_

- [ ] 12. 創建集成測試驗證完整功能
  - 編寫ScanService使用共享連接的集成測試
  - 測試多個掃描任務並發執行的正確性
  - 驗證事件處理和結果返回的一致性
  - 測試連接中斷和恢復場景下的系統行為
  - _Requirements: 2.1, 2.2, 2.3, 4.1, 4.2, 4.3_