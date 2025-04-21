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

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "執行 CSV 撥號批次",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("準備執行 CSV 批次：%s\n", csvPath)
		fmt.Printf("設置響鈴時間：%d 秒\n", ringTime)
		fmt.Printf("設置並發數量：%d\n", concurrency)
		fmt.Printf("設置撥號延遲：%d 毫秒\n", dialDelay) // 新增延遲輸出

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

		// 創建或開啟輸出檔案
		outFile, err := os.OpenFile("output.csv", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "無法開啟輸出檔案: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()
		writer := csv.NewWriter(outFile)
		defer writer.Flush()

		// 建立寫入用的互斥鎖
		var writerMutex sync.Mutex

		// 讀取已有的結果
		existingResults := make(map[string]string)
		if outputFile, err := os.Open("output.csv"); err == nil {
			defer outputFile.Close()
			outputReader := csv.NewReader(outputFile)
			if outputRecords, err := outputReader.ReadAll(); err == nil {
				for _, record := range outputRecords {
					if len(record) >= 2 {
						existingResults[record[0]] = record[1]
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
		var mu sync.Mutex
		done := make(chan struct{})
		// 用於追蹤號碼和 UUID 的對應關係
		numberToUUID := make(map[string]string)
		uuidToNumber := make(map[string]string)
		// 用於追蹤已處理的撥號任務
		processedNumbers := make(map[string]bool)

		// 修改 processAnswer 和 processHangup 函數，實現即時寫入
		processAnswerFunc := func(number string, results [][]string, records [][]string, processedNumbers map[string]bool, mu *sync.Mutex, done chan struct{}) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					mu.Lock()
					isProcessed := processedNumbers[number]
					if !isProcessed {
						results[i] = []string{number, "ANSWERED"}
						processedNumbers[number] = true
						log.Printf("結果已更新: 索引=%d, 號碼=%s, 結果=ANSWERED", i, number)

						// 即時寫入 CSV
						writerMutex.Lock()
						writer.Write([]string{number, "ANSWERED"})
						writer.Flush()
						writerMutex.Unlock()
						log.Printf("已即時寫入 CSV: %s -> ANSWERED", number)
					}
					mu.Unlock()
					break
				}
			}
			checkCompletion(processedNumbers, records, mu, done)
		}

		processHangupFunc := func(number, cause string, results [][]string, records [][]string, processedNumbers map[string]bool, mu *sync.Mutex, done chan struct{}, event *eslgo.Event) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					mu.Lock()
					isProcessed := processedNumbers[number]
					if !isProcessed {
						// 根據掛斷原因設置不同的結果
						var result string
						switch cause {

						case "NORMAL_CLEARING":
							result = "ANSWERED" // 正常通話結束
						default:
							result = cause // 其他原因直接使用原始原因
						}

						results[i] = []string{number, result}
						processedNumbers[number] = true
						log.Printf("結果已更新: 索引=%d, 號碼=%s, 結果=%s (原始原因: %s)", i, number, result, cause)

						// 即時寫入 CSV
						writerMutex.Lock()
						writer.Write([]string{number, result})
						writer.Flush()
						writerMutex.Unlock()
						log.Printf("已即時寫入 CSV: %s -> %s", number, result)
					}
					mu.Unlock()
					break
				}
			}
			checkCompletion(processedNumbers, records, mu, done)
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

			// 從事件中獲取號碼
			number := event.GetHeader("Other-Leg-Destination-Number")
			if number == "" {
				number = event.GetHeader("Caller-Destination-Number")
			}

			switch eventName {
			case "CHANNEL_CREATE":
				log.Printf("通話建立: UUID=%s, 號碼=%s", uuid, number)
				if number != "" {
					mu.Lock()
					numberToUUID[number] = uuid
					uuidToNumber[uuid] = number
					mu.Unlock()
				}
			case "CHANNEL_ANSWER":
				log.Printf("通話接聽: UUID=%s, 號碼=%s", uuid, number)
				if number == "" && uuid != "" {
					mu.Lock()
					number = uuidToNumber[uuid]
					mu.Unlock()
				}
				if number != "" {
					processAnswerFunc(number, results, records, processedNumbers, &mu, done)
				}
			case "CHANNEL_HANGUP":
				cause := event.GetHeader("Hangup-Cause")
				log.Printf("通話掛斷: UUID=%s, 號碼=%s, Cause=%s", uuid, number, cause)
				if number == "" && uuid != "" {
					mu.Lock()
					number = uuidToNumber[uuid]
					mu.Unlock()
				}
				if number != "" {
					processHangupFunc(number, cause, results, records, processedNumbers, &mu, done, event)
				}
			}
		})

		log.Println("開始撥打電話...")

		// 預先宣告所有後續會用到的變數，避免goto跳過變數宣告
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

		// 準備需要撥打的號碼清單
		for i, record := range records {
			if len(record) == 0 {
				continue
			}
			number := record[0]
			uuid := fmt.Sprintf("call-%d", i)

			// 檢查是否已有結果
			if status, exists := existingResults[number]; exists {
				log.Printf("跳過 #%d: %s (已有結果: %s)", i, number, status)
				mu.Lock()
				results[i] = []string{number, status}
				processedNumbers[number] = true
				mu.Unlock()
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

		// 創建同時執行的最大撥號數量的工作槽
		jobs = make(chan struct {
			index  int
			number string
			uuid   string
		}, concurrency)

		// 開始工作執行器
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				log.Printf("工作執行器 #%d 啟動", workerID)

				for job := range jobs {
					processDialJob(conn, job.index, job.number, job.uuid, ringTime, &mu, results, processedNumbers, &writerMutex, writer)
					// 加入延遲
					if dialDelay > 0 {
						log.Printf("工作執行器 #%d 延遲 %d 毫秒", workerID, dialDelay)
						time.Sleep(time.Duration(dialDelay) * time.Millisecond)
					}
				}

				log.Printf("工作執行器 #%d 結束", workerID)
			}(i)
		}

		// 發送工作到工作通道
		log.Printf("開始派發 %d 個撥號任務到 %d 個工作執行器", len(dialQueue), concurrency)
		for _, job := range dialQueue {
			jobs <- job
		}
		close(jobs) // 關閉工作通道，告知工作執行器沒有更多任務

		// 創建結果等待通道
		resultWait = make(chan struct{})
		go func() {
			wg.Wait() // 等待所有工作執行器結束
			close(resultWait)
		}()

		// 等待撥號完成或接收到結果
		log.Println("等待撥號結果...")

		// 設定一個額外的延遲，確保最後一個電話的結果可以被接收
		timeoutDuration = time.Duration(ringTime+15) * time.Second

		select {
		case <-done:
			log.Println("所有電話結果已接收")
		case <-resultWait:
			log.Println("所有撥號任務已完成，等待額外5秒接收最後的結果...")
			// 增加一個額外的等待時間，確保最後一個電話的結果可以被接收
			select {
			case <-done:
				log.Println("在額外等待期間，所有電話結果已接收")
			case <-time.After(5 * time.Second):
				log.Println("額外等待時間結束，繼續處理")
			}
		case <-time.After(timeoutDuration):
			log.Println("等待結果逾時，將使用已有的結果")
		}

	finish:
		// 移除事件監聽器並關閉連接
		conn.RemoveEventListener(eslgo.EventListenAll, listenerID)
		conn.Close()

		// 檢查並寫入任何未處理的號碼結果
		log.Println("檢查是否有未處理的號碼...")
		for _, record := range records {
			if len(record) > 0 {
				number := record[0]
				mu.Lock()
				isProcessed := processedNumbers[number]
				mu.Unlock()

				if !isProcessed {
					log.Printf("號碼 %s 無結果，標記為 NO_RESPONSE", number)
					writerMutex.Lock()
					writer.Write([]string{number, "NO_RESPONSE"})
					writer.Flush()
					writerMutex.Unlock()
					log.Printf("已即時寫入 CSV: %s -> NO_RESPONSE", number)
				}
			}
		}

		fmt.Println("處理完成，結果已寫入 output.csv")
	},
}

// 處理單個撥號任務
func processDialJob(conn *eslgo.Conn, index int, number, uuid string, ringTime int, mu *sync.Mutex, results [][]string, processedNumbers map[string]bool, writerMutex *sync.Mutex, writer *csv.Writer) {
	log.Printf("開始處理撥號任務 #%d: %s (UUID: %s)", index, number, uuid)

	// 設置此撥號任務正在處理中的標記
	mu.Lock()
	isAlreadyProcessed := processedNumbers[number]
	mu.Unlock()

	if isAlreadyProcessed {
		log.Printf("跳過已處理的號碼 #%d: %s", index, number)
		return
	}

	// 執行撥號
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
		mu.Lock()
		if !processedNumbers[number] {
			results[index] = []string{number, "originate_failed"}
			processedNumbers[number] = true

			// 即時寫入 CSV
			writerMutex.Lock()
			writer.Write([]string{number, "originate_failed"})
			writer.Flush()
			writerMutex.Unlock()
			log.Printf("已即時寫入 CSV: %s -> originate_failed", number)
		}
		mu.Unlock()
	} else {
		log.Printf("撥號成功 #%d: %s, 等待結果", index, number)
		// 等待ringTime+2秒，確保能收到撥號結果
		waitTime := time.Duration(ringTime+2) * time.Second
		startTime := time.Now()

		// 等待直到收到結果或等待時間結束
		for time.Since(startTime) < waitTime {
			mu.Lock()
			isProcessed := processedNumbers[number]
			mu.Unlock()

			if isProcessed {
				log.Printf("已收到號碼 #%d: %s 的結果，不再等待", index, number)
				break
			}
			time.Sleep(500 * time.Millisecond) // 短暫休眠避免過度消耗CPU
		}

		// 再次檢查有沒有收到結果
		mu.Lock()
		isProcessed := processedNumbers[number]
		mu.Unlock()

		// 如果沒有收到結果，設為 NO_ANSWER
		if !isProcessed {
			log.Printf("等待時間結束，號碼 #%d: %s 仍無結果，標記為 NO_ANSWER", index, number)
			mu.Lock()
			results[index] = []string{number, "NO_ANSWER"}
			processedNumbers[number] = true
			mu.Unlock()

			// 即時寫入 CSV
			writerMutex.Lock()
			writer.Write([]string{number, "NO_ANSWER"})
			writer.Flush()
			writerMutex.Unlock()
			log.Printf("已即時寫入 CSV: %s -> NO_ANSWER", number)
		}
	}

	log.Printf("撥號任務 #%d 處理完成", index)
}

func checkCompletion(processedNumbers map[string]bool, records [][]string, mu *sync.Mutex, done chan struct{}) {
	mu.Lock()

	// 計算實際有效的號碼數量
	validNumbersCount := 0
	for _, record := range records {
		if len(record) > 0 && record[0] != "" {
			validNumbersCount++
		}
	}

	// 計算已處理的號碼數量
	processedCount := len(processedNumbers)

	log.Printf("已完成 %d/%d 通電話", processedCount, validNumbersCount)

	if processedCount >= validNumbersCount {
		select {
		case <-done:
			// 通道已關閉，不需要再次關閉
		default:
			log.Println("全部通話已完成，關閉 done 通道")
			close(done)
		}
	}
	mu.Unlock()
}
