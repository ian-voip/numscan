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
)

func init() {
	scanCmd.Flags().StringVarP(&csvPath, "csv", "c", "", "CSV 檔案路徑 (必填)")
	scanCmd.Flags().IntVarP(&ringTime, "ring", "r", 30, "響鈴時間（秒）")
	scanCmd.Flags().IntVarP(&concurrency, "concurrency", "n", 1, "並發撥號數量")
	scanCmd.MarkFlagRequired("csv")
	rootCmd.AddCommand(scanCmd)
}

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "執行 CSV 撥號批次",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("準備執行 CSV 批次：%s\n", csvPath)
		fmt.Printf("設置響鈴時間：%d 秒\n", ringTime)

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
					numberToUUID[number] = uuid
					uuidToNumber[uuid] = number
				}
			case "CHANNEL_ANSWER":
				log.Printf("通話接聽: UUID=%s, 號碼=%s", uuid, number)
				if number == "" && uuid != "" {
					number = uuidToNumber[uuid]
				}
				if number != "" {
					processAnswer(number, results, records, &mu, done)
				}
			case "CHANNEL_HANGUP":
				cause := event.GetHeader("Hangup-Cause")
				log.Printf("通話掛斷: UUID=%s, 號碼=%s, Cause=%s", uuid, number, cause)
				if number == "" && uuid != "" {
					number = uuidToNumber[uuid]
				}
				if number != "" {
					processHangup(number, cause, results, records, &mu, done, event)
				}
			}
		})

		log.Println("開始撥打電話...")
		semaphore := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var hasNewCalls bool

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
				mu.Unlock()
				continue
			}

			hasNewCalls = true
			log.Printf("撥打 #%d: %s (UUID: %s)", i, number, uuid)

			wg.Add(1)
			semaphore <- struct{}{} // 獲取信號量
			go func(i int, number, uuid string) {
				defer wg.Done()
				defer func() { <-semaphore }() // 釋放信號量

				aLeg := eslgo.Leg{CallURL: "null"}
				bLeg := eslgo.Leg{CallURL: fmt.Sprintf("%s XML numscan", number)}
				vars := map[string]string{
					"origination_uuid":             uuid,
					"ignore_early_media":           "true",
					"origination_caller_id_name":   "CLI",
					"origination_caller_id_number": "1000",
					"call_timeout":                 fmt.Sprintf("%d", ringTime),
					"execute_on_answer":            "uuid_kill ${uuid}",
				}

				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ringTime+5)*time.Second)
				_, err := conn.OriginateCall(ctx, true, aLeg, bLeg, vars)
				defer cancel()

				if err != nil {
					log.Printf("撥號失敗 #%d: %s, 錯誤: %v", i, number, err)
					mu.Lock()
					results[i] = []string{number, "originate_failed"}
					mu.Unlock()
				} else {
					log.Printf("撥號成功 #%d: %s, 等待結果", i, number)
				}
			}(i, number, uuid)
		}

		if !hasNewCalls {
			log.Println("所有號碼已有結果，無需撥號")
			goto writeResults
		}

		wg.Wait() // 等待所有撥號完成
		log.Println("所有號碼已啟動撥號，等待結果...")
		select {
		case <-done:
			log.Println("所有電話已正常完成")
		case <-time.After(time.Duration(ringTime+10) * time.Second):
			log.Println("等待結果逾時，將使用已有的結果")
		}

	writeResults:
		// 移除事件監聽器並關閉連接
		conn.RemoveEventListener(eslgo.EventListenAll, listenerID)
		conn.Close()

		outFile, err := os.Create("output.csv")
		if err != nil {
			fmt.Fprintf(os.Stderr, "無法建立輸出檔案: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()

		writer := csv.NewWriter(outFile)
		defer writer.Flush()

		for i, row := range results {
			if len(row) > 0 {
				if err := writer.Write(row); err != nil {
					fmt.Fprintf(os.Stderr, "寫入 CSV 時發生錯誤: %v\n", err)
				}
			} else if i < len(records) && len(records[i]) > 0 {
				if err := writer.Write([]string{records[i][0], "NO_RESPONSE"}); err != nil {
					fmt.Fprintf(os.Stderr, "寫入 CSV 時發生錯誤: %v\n", err)
				}
			}
		}

		fmt.Println("處理完成，結果已寫入 output.csv")
	},
}

func processAnswer(number string, results [][]string, records [][]string, mu *sync.Mutex, done chan struct{}) {
	for i, record := range records {
		if len(record) > 0 && record[0] == number {
			mu.Lock()
			results[i] = []string{number, "ANSWERED"}
			log.Printf("結果已更新: 索引=%d, 號碼=%s, 結果=ANSWERED", i, number)
			mu.Unlock()
			break
		}
	}
	checkCompletion(results, records, mu, done)
}

func processHangup(number, cause string, results [][]string, records [][]string, mu *sync.Mutex, done chan struct{}, event *eslgo.Event) {
	for i, record := range records {
		if len(record) > 0 && record[0] == number {
			if len(results[i]) == 0 {
				mu.Lock()
				results[i] = []string{number, cause}
				log.Printf("結果已更新: 索引=%d, 號碼=%s, 結果=%s", i, number, cause)
				mu.Unlock()
			}
			break
		}
	}
	checkCompletion(results, records, mu, done)
}

func checkCompletion(results [][]string, records [][]string, mu *sync.Mutex, done chan struct{}) {
	mu.Lock()
	count := 0
	for _, row := range results {
		if len(row) > 0 {
			count++
		}
	}
	log.Printf("已完成 %d/%d 通電話", count, len(records))
	if count == len(records) {
		log.Println("全部通話已完成，關閉 done 通道")
		close(done)
	}
	mu.Unlock()
}
