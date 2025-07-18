package cmd

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/percipia/eslgo"
	"github.com/spf13/cobra"
)

var (
	csvPath     string
	ringTime    int
	concurrency int
	dialDelay   int // 新增撥號延遲變數 (毫秒)
)

func init() {
	scanCmd.Flags().StringVarP(&csvPath, "csv", "c", "", "CSV 檔案路徑 (必填)")
	scanCmd.Flags().IntVarP(&ringTime, "ring", "r", 30, "響鈴時間（秒）")
	scanCmd.Flags().IntVarP(&concurrency, "concurrency", "n", 1, "並發撥號數量")
	scanCmd.Flags().IntVarP(&dialDelay, "delay", "d", 100, "撥號間隔延遲（毫秒）") // 新增延遲參數
	scanCmd.MarkFlagRequired("csv")
	rootCmd.AddCommand(scanCmd)
}

// 安全的結果寫入器，統一管理鎖的順序
type safeResultWriter struct {
	mu               sync.Mutex
	writerMutex      sync.Mutex
	writer           *csv.Writer
	results          [][]string
	processedNumbers map[string]bool
	closeOnce        sync.Once
	done             chan struct{}
	records          [][]string
}

func newSafeResultWriter(writer *csv.Writer, results [][]string, records [][]string) *safeResultWriter {
	return &safeResultWriter{
		writer:           writer,
		results:          results,
		processedNumbers: make(map[string]bool),
		done:             make(chan struct{}),
		records:          records,
	}
}

func (w *safeResultWriter) writeResult(index int, number, result string) {
	w.writeResultWithDetails(index, number, result, "", true)
}

func (w *safeResultWriter) writeResultWithDetails(index int, number, result, details string, writeToCSV bool) {
	// 統一鎖的順序：先 mu，後 writerMutex
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.processedNumbers[number] {
		// 如果號碼已處理，但這次有更詳細的資訊，就允許覆寫
		if details != "" {
			log.Printf("號碼 %s 已有結果，但收到更詳細的掛斷資訊，準備更新", number)
		} else {
			return // 已處理過，且沒有更詳細資訊
		}
	}

	var csvRow []string
	if result == "ALLOTTED_TIMEOUT" {
		csvRow = []string{number, "TIMEOUT", "ALLOTTED_TIMEOUT"}
		w.results[index] = csvRow
	} else if details != "" {
		csvRow = []string{number, result, details}
		w.results[index] = csvRow
	} else {
		csvRow = []string{number, result}
		w.results[index] = csvRow
	}

	w.processedNumbers[number] = true
	log.Printf("結果已更新: 索引=%d, 號碼=%s, 結果=%s, 詳細=%s", index, number, result, details)

	if writeToCSV {
		// 即時寫入 CSV
		w.writerMutex.Lock()
		w.writer.Write(csvRow)
		w.writer.Flush()
		w.writerMutex.Unlock()
		log.Printf("已即時寫入 CSV: %s -> %s -> %s", number, result, details)
	} else {
		log.Printf("已載入現有結果: %s -> %s -> %s", number, result, details)
	}

	w.checkCompletion()
}

// 用於處理已存在結果的號碼，不重複寫入 CSV
func (w *safeResultWriter) markExistingResult(index int, number, result string) {
	w.writeResultWithDetails(index, number, result, "", false)
}

// 用於處理有詳細信息的已存在結果
func (w *safeResultWriter) markExistingResultWithDetails(index int, number, result, details string) {
	w.writeResultWithDetails(index, number, result, details, false)
}

func (w *safeResultWriter) isProcessed(number string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.processedNumbers[number]
}

func (w *safeResultWriter) checkCompletion() {
	// 注意：此函數必須在持有 mu 鎖的情況下調用
	validNumbersCount := 0
	for _, record := range w.records {
		if len(record) > 0 && record[0] != "" {
			validNumbersCount++
		}
	}

	processedCount := len(w.processedNumbers)

	log.Printf("已完成 %d/%d 通電話", processedCount, validNumbersCount)

	if processedCount >= validNumbersCount {
		w.closeOnce.Do(func() {
			log.Println("全部通話已完成，關閉 done 通道")
			close(w.done)
		})
	}
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "執行 CSV 撥號批次",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("準備執行 CSV 批次：%s\n", csvPath)
		fmt.Printf("設置響鈴時間：%d 秒\n", ringTime)
		fmt.Printf("設置並發數量：%d\n", concurrency)
		fmt.Printf("設置撥號延遲：%d 毫秒\n", dialDelay)

		file, err := os.Open(csvPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "無法開啟 CSV 檔案: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()

		reader := csv.NewReader(file)
		records, err := reader.ReadAll()
		if err != nil {
			fmt.Fprintf(os.Stderr, "讀取 CSV 失敗: %v\n", err)
			os.Exit(1)
		}

		outFile, err := os.OpenFile("output.csv", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "無法開啟輸出檔案: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()
		writer := csv.NewWriter(outFile)
		defer writer.Flush()

		existingResults := make(map[string][]string)
		if outputFile, err := os.Open("output.csv"); err == nil {
			defer outputFile.Close()
			outputReader := csv.NewReader(outputFile)
			if outputRecords, err := outputReader.ReadAll(); err == nil {
				for _, record := range outputRecords {
					if len(record) >= 2 {
						existingResults[record[0]] = record
					}
				}
			}
		}

		conn, err := eslgo.Dial("64.52.80.198:8021", "tomoon", func() {
			log.Printf("與 FreeSWITCH 中斷連線\n")
		})
		if err != nil {
			log.Println("Error connecting", err)
			os.Exit(1)
		}
		defer conn.ExitAndClose()
		log.Println("成功連接到 FreeSWITCH")

		if err := conn.EnableEvents(context.Background()); err != nil {
			log.Fatalf("啟用事件接收失敗: %v", err)
		}

		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)

		var uuidMutex sync.RWMutex
		numberToUUID := make(map[string]string)
		uuidToNumber := make(map[string]string)
		// ⭐️ [修改 1] 新增一個 map 來追蹤已接聽的號碼
		answeredNumbers := make(map[string]bool)

		// ⭐️ [修改 2] processAnswerFunc 不再寫入檔案，只記錄狀態
		processAnswerFunc := func(number string) {
			uuidMutex.Lock()
			answeredNumbers[number] = true
			uuidMutex.Unlock()
			log.Printf("號碼 %s 已接聽 (狀態已記錄)", number)
		}

		// ⭐️ [修改 3] processHangupFunc 成為唯一的結果寫入點
		processHangupFunc := func(number, cause string) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					if resultWriter.isProcessed(number) {
						// 雖然理論上不應該在掛斷時才處理已處理過的號碼，但作為保險
						// 允許 ALLOTTED_TIMEOUT 覆寫之前的結果
						if cause != "ALLOTTED_TIMEOUT" {
							log.Printf("號碼 %s 已被處理過，且掛斷原因非 ALLOTTED_TIMEOUT，跳過", number)
							return
						}
					}

					uuidMutex.RLock()
					isAnswered := answeredNumbers[number]
					uuidMutex.RUnlock()

					// 核心邏輯：根據是否接聽過和掛斷原因來寫入結果
					if cause == "ALLOTTED_TIMEOUT" {
						resultWriter.writeResultWithDetails(i, number, "ANSWERED", "ALLOTTED_TIMEOUT", true)
					} else if isAnswered {
						// 如果接聽過，但有其他掛斷原因 (例如對方提前掛斷)
						resultWriter.writeResultWithDetails(i, number, "ANSWERED", cause, true)
					} else {
						// 如果從未接聽就掛斷了 (例如無應答、忙線)
						resultWriter.writeResult(i, number, cause)
					}
					break
				}
			}
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

		log.Println("開始撥打電話...")

		var hasNewCalls bool
		var dialQueue []struct {
			index  int
			number string
			uuid   string
		}
		var totalDialCount int
		var jobs chan struct {
			index  int
			number string
			uuid   string
		}
		var wg sync.WaitGroup
		var resultWait chan struct{}
		var timeoutDuration time.Duration
		var totalEstimatedTime time.Duration

		for i, record := range records {
			if len(record) == 0 {
				continue
			}
			number := record[0]
			uuid := fmt.Sprintf("call-%d", i)

			if status, exists := existingResults[number]; exists {
				if len(status) >= 3 {
					log.Printf("跳過 #%d: %s (已有結果: %s, 詳細: %s)", i, number, status[1], status[2])
					resultWriter.markExistingResultWithDetails(i, number, status[1], status[2])
				} else if len(status) >= 2 {
					log.Printf("跳過 #%d: %s (已有結果: %s)", i, number, status[1])
					resultWriter.markExistingResult(i, number, status[1])
				}
				continue
			}

			hasNewCalls = true
			totalDialCount++
			dialQueue = append(dialQueue, struct {
				index  int
				number string
				uuid   string
			}{i, number, uuid})
		}

		if !hasNewCalls {
			log.Println("所有號碼已有結果，無需撥號")
			goto finish
		}

		jobs = make(chan struct {
			index  int
			number string
			uuid   string
		}, len(dialQueue))

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				log.Printf("工作執行器 #%d 啟動", workerID)

				for job := range jobs {
					processDialJob(conn, job.index, job.number, job.uuid, ringTime, resultWriter)
					if dialDelay > 0 {
						log.Printf("工作執行器 #%d 延遲 %d 毫秒", workerID, dialDelay)
						time.Sleep(time.Duration(dialDelay) * time.Millisecond)
					}
				}

				log.Printf("工作執行器 #%d 結束", workerID)
			}(i)
		}

		log.Printf("開始派發 %d 個撥號任務到 %d 個工作執行器", len(dialQueue), concurrency)
		for _, job := range dialQueue {
			jobs <- job
		}
		close(jobs)

		resultWait = make(chan struct{})
		go func() {
			wg.Wait()
			close(resultWait)
		}()

		log.Println("等待撥號結果...")

		totalEstimatedTime = time.Duration(totalDialCount/concurrency+1) * time.Duration(ringTime) * time.Second
		if dialDelay > 0 {
			totalEstimatedTime += time.Duration(totalDialCount*dialDelay) * time.Millisecond
		}
		timeoutDuration = totalEstimatedTime + 30*time.Second

		log.Printf("預估總處理時間: %v, 設定超時時間: %v", totalEstimatedTime, timeoutDuration)

		select {
		case <-resultWriter.done:
			log.Println("所有電話結果已接收")
		case <-resultWait:
			log.Println("所有撥號任務已完成，等待額外20秒接收最後的結果...")
			select {
			case <-resultWriter.done:
				log.Println("在額外等待期間，所有電話結果已接收")
			case <-time.After(20 * time.Second):
				log.Println("額外等待時間結束，繼續處理")
			}
		case <-time.After(timeoutDuration):
			log.Printf("等待結果逾時（%v），將使用已有的結果", timeoutDuration)
		}

	finish:
		conn.RemoveEventListener(eslgo.EventListenAll, listenerID)
		conn.Close()

		log.Println("檢查是否有未處理的號碼...")
		unprocessedCount := 0
		for i, record := range records {
			if len(record) > 0 {
				number := record[0]
				if !resultWriter.isProcessed(number) {
					if existingRecord, exists := existingResults[number]; exists {
						log.Printf("號碼 %s 在已有結果中但未被正確載入，重新標記", number)
						if len(existingRecord) >= 3 {
							resultWriter.markExistingResultWithDetails(i, number, existingRecord[1], existingRecord[2])
						} else if len(existingRecord) >= 2 {
							resultWriter.markExistingResult(i, number, existingRecord[1])
						}
					} else {
						unprocessedCount++
						log.Printf("號碼 %s 無結果（第%d個未處理），標記為 TIMEOUT", number, unprocessedCount)
						resultWriter.writeResult(i, number, "TIMEOUT")
					}
				}
			}
		}

		if unprocessedCount > 0 {
			log.Printf("警告：有 %d 個號碼未收到結果，可能需要調整超時時間或檢查網路連接", unprocessedCount)
		}

		totalNumbers := 0
		processedNumbers := 0
		for _, record := range records {
			if len(record) > 0 && record[0] != "" {
				totalNumbers++
				if resultWriter.isProcessed(record[0]) {
					processedNumbers++
				}
			}
		}

		fmt.Printf("處理完成！總號碼數: %d, 已處理: %d, 結果已寫入 output.csv\n",
			totalNumbers, processedNumbers)
	},
}

// 處理單個撥號任務
func processDialJob(conn *eslgo.Conn, index int, number, uuid string, ringTime int, resultWriter *safeResultWriter) {
	log.Printf("開始處理撥號任務 #%d: %s (UUID: %s)", index, number, uuid)

	if resultWriter.isProcessed(number) {
		log.Printf("跳過已處理的號碼 #%d: %s", index, number)
		return
	}

	aLeg := eslgo.Leg{CallURL: "null"}
	bLeg := eslgo.Leg{CallURL: fmt.Sprintf("%s XML numscan", number)}
	vars := map[string]string{
		"origination_uuid":             uuid,
		"ignore_early_media":           "true",
		"origination_caller_id_name":   "CLI",
		"origination_caller_id_number": "1000",
		"call_timeout":                 fmt.Sprintf("%d", ringTime),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ringTime+5)*time.Second)
	_, err := conn.OriginateCall(ctx, true, aLeg, bLeg, vars)
	defer cancel()

	if err != nil {
		log.Printf("撥號失敗 #%d: %s, 錯誤: %v", index, number, err)
		resultWriter.writeResult(index, number, "originate_failed")
	} else {
		log.Printf("撥號成功 #%d: %s, 等待結果", index, number)
		waitTime := time.Duration(ringTime+5) * time.Second
		startTime := time.Now()

		for time.Since(startTime) < waitTime {
			if resultWriter.isProcessed(number) {
				log.Printf("已收到號碼 #%d: %s 的結果，不再等待", index, number)
				return
			}
			time.Sleep(200 * time.Millisecond)
		}

		log.Printf("撥號任務 #%d: %s 等待時間結束，將由主程序統一處理未收到結果的號碼", index, number)
	}

	log.Printf("撥號任務 #%d 處理完成", index)
}
