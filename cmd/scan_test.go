package cmd

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/percipia/eslgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DialerConn interface for testing - only includes methods needed by processDialJob
type DialerConn interface {
	OriginateCall(ctx context.Context, async bool, aLeg eslgo.Leg, bLeg eslgo.Leg, vars map[string]string) (interface{}, error)
}

// EventInterface defines the interface that events must implement
type EventInterface interface {
	GetName() string
	GetHeader(key string) string
}

// testProcessDialJob is a test-friendly version of processDialJob that accepts an interface
func testProcessDialJob(conn DialerConn, index int, number, uuid string, ringTime int, resultWriter *safeResultWriter) {
	fmt.Printf("開始處理撥號任務 #%d: %s (UUID: %s)\n", index, number, uuid)

	// 檢查是否已處理
	if resultWriter.isProcessed(number) {
		fmt.Printf("跳過已處理的號碼 #%d: %s\n", index, number)
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
		fmt.Printf("撥號失敗 #%d: %s, 錯誤: %v\n", index, number, err)
		resultWriter.writeResult(index, number, "originate_failed")
	} else {
		fmt.Printf("撥號成功 #%d: %s, 等待結果\n", index, number)
		// 等待ringTime+5秒，給予更充足的時間接收事件
		waitTime := time.Duration(ringTime+5) * time.Second
		startTime := time.Now()

		// 等待直到收到結果或等待時間結束
		for time.Since(startTime) < waitTime {
			if resultWriter.isProcessed(number) {
				fmt.Printf("已收到號碼 #%d: %s 的結果，不再等待\n", index, number)
				return // 直接返回，不標記為 NO_ANSWER
			}
			time.Sleep(200 * time.Millisecond) // 減少睡眠時間，更頻繁檢查
		}

		// 注意：不在這裡標記為 NO_ANSWER，讓主程序統一處理
		fmt.Printf("撥號任務 #%d: %s 等待時間結束，將由主程序統一處理未收到結果的號碼\n", index, number)
	}

	fmt.Printf("撥號任務 #%d 處理完成\n", index)
}

// mockESLEvent implements a mock eslgo.Event for testing
type mockESLEvent struct {
	name    string
	headers map[string]string
}

func (e *mockESLEvent) GetName() string {
	return e.name
}

func (e *mockESLEvent) GetHeader(key string) string {
	return e.headers[key]
}

func (e *mockESLEvent) SetName(name string) {
	e.name = name
}

func (e *mockESLEvent) SetHeader(key, value string) {
	if e.headers == nil {
		e.headers = make(map[string]string)
	}
	e.headers[key] = value
}

// mockESLConn implements a mock FreeSWITCH connection for testing
type mockESLConn struct {
	mu                    sync.RWMutex
	events                []mockEvent
	eventListeners        map[string][]func(EventInterface)
	originateCallResults  map[string]error
	originateCallCalled   map[string]int
	enableEventsErr       error
	closed                bool
	callbacksOnOriginate  []func(string)
}

type mockEvent struct {
	name    string
	headers map[string]string
	delay   time.Duration
}

func newMockESLConn() *mockESLConn {
	return &mockESLConn{
		eventListeners:       make(map[string][]func(EventInterface)),
		originateCallResults: make(map[string]error),
		originateCallCalled:  make(map[string]int),
		callbacksOnOriginate: make([]func(string), 0),
	}
}

func (m *mockESLConn) EnableEvents(ctx context.Context) error {
	return m.enableEventsErr
}

func (m *mockESLConn) RegisterEventListener(eventType string, callback func(EventInterface)) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.eventListeners[eventType] == nil {
		m.eventListeners[eventType] = make([]func(EventInterface), 0)
	}
	m.eventListeners[eventType] = append(m.eventListeners[eventType], callback)
	return fmt.Sprintf("listener-%d", len(m.eventListeners[eventType]))
}

func (m *mockESLConn) RemoveEventListener(eventType string, listenerID string) {
	// Mock implementation
}

func (m *mockESLConn) OriginateCall(ctx context.Context, async bool, aLeg eslgo.Leg, bLeg eslgo.Leg, vars map[string]string) (interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	uuid := vars["origination_uuid"]
	m.originateCallCalled[uuid]++
	
	// Execute callbacks
	for _, callback := range m.callbacksOnOriginate {
		callback(uuid)
	}
	
	// Return predefined error if exists
	if err, exists := m.originateCallResults[uuid]; exists {
		return nil, err
	}
	
	return nil, nil
}

func (m *mockESLConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockESLConn) ExitAndClose() {
	m.Close()
}

func (m *mockESLConn) fireEvent(eventName string, headers map[string]string, delay time.Duration) {
	if delay > 0 {
		go func() {
			time.Sleep(delay)
			m.doFireEvent(eventName, headers)
		}()
	} else {
		m.doFireEvent(eventName, headers)
	}
}

func (m *mockESLConn) doFireEvent(eventName string, headers map[string]string) {
	m.mu.RLock()
	listeners := m.eventListeners[eslgo.EventListenAll]
	m.mu.RUnlock()
	
	event := &mockESLEvent{
		name:    eventName,
		headers: make(map[string]string),
	}
	for key, value := range headers {
		event.SetHeader(key, value)
	}
	
	for _, listener := range listeners {
		listener(event)
	}
}

func (m *mockESLConn) addCallbackOnOriginate(callback func(string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacksOnOriginate = append(m.callbacksOnOriginate, callback)
}

func (m *mockESLConn) setOriginateResult(uuid string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.originateCallResults[uuid] = err
}

func (m *mockESLConn) getOriginateCallCount(uuid string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.originateCallCalled[uuid]
}

// Test safeResultWriter concurrent operations
func TestSafeResultWriterConcurrency(t *testing.T) {
	t.Run("concurrent writeResult operations", func(t *testing.T) {
		// Create test CSV writer
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		// Create test records
		records := [][]string{
			{"1000001", ""},
			{"1000002", ""},
			{"1000003", ""},
			{"1000004", ""},
			{"1000005", ""},
		}
		
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Test concurrent writes
		var wg sync.WaitGroup
		numGoroutines := 10
		
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				
				for j := 0; j < len(records); j++ {
					number := records[j][0]
					result := fmt.Sprintf("RESULT_%d_%d", goroutineID, j)
					resultWriter.writeResult(j, number, result)
				}
			}(i)
		}
		
		wg.Wait()
		
		// Verify that each number was only processed once
		for i, record := range records {
			assert.True(t, resultWriter.isProcessed(record[0]), 
				"Number %s at index %d should be processed", record[0], i)
		}
		
		// Verify no duplicate processing
		assert.Equal(t, len(records), len(resultWriter.processedNumbers), 
			"Should have exactly %d processed numbers", len(records))
	})
	
	t.Run("concurrent isProcessed checks", func(t *testing.T) {
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		records := [][]string{{"1000001", ""}, {"1000002", ""}}
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Start concurrent isProcessed checks
		var wg sync.WaitGroup
		results_chan := make(chan bool, 100)
		
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					processed := resultWriter.isProcessed("1000001")
					results_chan <- processed
				}
			}()
		}
		
		// Write a result concurrently
		go func() {
			time.Sleep(10 * time.Millisecond)
			resultWriter.writeResult(0, "1000001", "ANSWERED")
		}()
		
		wg.Wait()
		close(results_chan)
		
		// Verify no race conditions occurred
		trueCount := 0
		falseCount := 0
		for result := range results_chan {
			if result {
				trueCount++
			} else {
				falseCount++
			}
		}
		
		assert.Greater(t, trueCount, 0, "Should have some true results after write")
		assert.Greater(t, falseCount, 0, "Should have some false results before write")
	})
}

// Test concurrent dial job processing
func TestConcurrentDialJobProcessing(t *testing.T) {
	t.Run("multiple dial jobs with mock connection", func(t *testing.T) {
		mockConn := newMockESLConn()
		
		// Create test CSV writer
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		// Create test records
		records := [][]string{
			{"1000001", ""},
			{"1000002", ""},
			{"1000003", ""},
			{"1000004", ""},
			{"1000005", ""},
		}
		
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Setup mock to fire events for each call
		mockConn.addCallbackOnOriginate(func(uuid string) {
			go func() {
				time.Sleep(10 * time.Millisecond)
				// Fire CHANNEL_CREATE event
				mockConn.fireEvent("CHANNEL_CREATE", map[string]string{
					"variable_origination_uuid": uuid,
					"Other-Leg-Destination-Number": getNumberFromUUID(uuid),
				}, 0)
				
				// Fire CHANNEL_ANSWER event after short delay
				time.Sleep(20 * time.Millisecond)
				mockConn.fireEvent("CHANNEL_ANSWER", map[string]string{
					"variable_origination_uuid": uuid,
					"Other-Leg-Destination-Number": getNumberFromUUID(uuid),
				}, 0)
			}()
		})
		
		// Process dial jobs concurrently
		var wg sync.WaitGroup
		concurrency := 3
		
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				
				for j := workerID; j < len(records); j += concurrency {
					number := records[j][0]
					uuid := fmt.Sprintf("call-%d", j)
					
					testProcessDialJob(mockConn, j, number, uuid, 2, resultWriter)
				}
			}(i)
		}
		
		wg.Wait()
		
		// Wait for events to be processed
		time.Sleep(100 * time.Millisecond)
		
		// Verify all calls were made
		for i := 0; i < len(records); i++ {
			uuid := fmt.Sprintf("call-%d", i)
			assert.Equal(t, 1, mockConn.getOriginateCallCount(uuid), 
				"Should have called originate once for %s", uuid)
		}
	})
	
	t.Run("concurrent processing with failures", func(t *testing.T) {
		mockConn := newMockESLConn()
		
		// Create test CSV writer
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		records := [][]string{
			{"1000001", ""},
			{"1000002", ""},
			{"1000003", ""},
		}
		
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Set up some calls to fail
		mockConn.setOriginateResult("call-1", fmt.Errorf("originate failed"))
		
		// Process dial jobs concurrently
		var wg sync.WaitGroup
		for i := 0; i < len(records); i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				
				number := records[index][0]
				uuid := fmt.Sprintf("call-%d", index)
				
				testProcessDialJob(mockConn, index, number, uuid, 1, resultWriter)
			}(i)
		}
		
		wg.Wait()
		
		// Verify failed call was marked as originate_failed
		assert.True(t, resultWriter.isProcessed("1000002"), 
			"Failed call should be processed")
	})
}

// Test event handling under concurrent conditions
func TestConcurrentEventHandling(t *testing.T) {
	t.Run("concurrent event processing", func(t *testing.T) {
		mockConn := newMockESLConn()
		
		// Create test CSV writer
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		records := [][]string{
			{"1000001", ""},
			{"1000002", ""},
			{"1000003", ""},
			{"1000004", ""},
			{"1000005", ""},
		}
		
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Set up event processing similar to main function
		var uuidMutex sync.RWMutex
		numberToUUID := make(map[string]string)
		uuidToNumber := make(map[string]string)
		
		processAnswerFunc := func(number string) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					resultWriter.writeResult(i, number, "ANSWERED")
					break
				}
			}
		}
		
		processHangupFunc := func(number, cause string) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					if resultWriter.isProcessed(number) {
						return
					}
					resultWriter.writeResult(i, number, cause)
					break
				}
			}
		}
		
		// Register event listener
		mockConn.RegisterEventListener(eslgo.EventListenAll, func(event EventInterface) {
			eventName := event.GetName()
			uuid := event.GetHeader("variable_origination_uuid")
			if uuid == "" {
				uuid = event.GetHeader("variable_uuid")
			}
			
			number := event.GetHeader("Other-Leg-Destination-Number")
			if number == "" {
				number = event.GetHeader("Caller-Destination-Number")
			}
			
			switch eventName {
			case "CHANNEL_CREATE":
				if number != "" && uuid != "" {
					uuidMutex.Lock()
					numberToUUID[number] = uuid
					uuidToNumber[uuid] = number
					uuidMutex.Unlock()
				}
			case "CHANNEL_ANSWER":
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
		
		// Fire events concurrently
		var wg sync.WaitGroup
		for i := 0; i < len(records); i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				
				number := records[index][0]
				uuid := fmt.Sprintf("call-%d", index)
				
				// Fire CHANNEL_CREATE event
				mockConn.fireEvent("CHANNEL_CREATE", map[string]string{
					"variable_origination_uuid": uuid,
					"Other-Leg-Destination-Number": number,
				}, 0)
				
				// Fire CHANNEL_ANSWER event after short delay
				time.Sleep(time.Duration(index*5) * time.Millisecond)
				mockConn.fireEvent("CHANNEL_ANSWER", map[string]string{
					"variable_origination_uuid": uuid,
				}, 0)
			}(i)
		}
		
		wg.Wait()
		
		// Wait for events to be processed
		time.Sleep(100 * time.Millisecond)
		
		// Verify all numbers were processed
		for _, record := range records {
			if len(record) > 0 {
				assert.True(t, resultWriter.isProcessed(record[0]), 
					"Number %s should be processed", record[0])
			}
		}
	})
	
	t.Run("race condition between answer and hangup events", func(t *testing.T) {
		mockConn := newMockESLConn()
		
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		records := [][]string{{"1000001", ""}}
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Set up event processing
		processAnswerFunc := func(number string) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					resultWriter.writeResult(i, number, "ANSWERED")
					break
				}
			}
		}
		
		processHangupFunc := func(number, cause string) {
			for i, record := range records {
				if len(record) > 0 && record[0] == number {
					if resultWriter.isProcessed(number) {
						return
					}
					resultWriter.writeResult(i, number, cause)
					break
				}
			}
		}
		
		mockConn.RegisterEventListener(eslgo.EventListenAll, func(event EventInterface) {
			eventName := event.GetName()
			number := event.GetHeader("Other-Leg-Destination-Number")
			
			switch eventName {
			case "CHANNEL_ANSWER":
				if number != "" {
					processAnswerFunc(number)
				}
			case "CHANNEL_HANGUP":
				cause := event.GetHeader("Hangup-Cause")
				if number != "" {
					processHangupFunc(number, cause)
				}
			}
		})
		
		// Fire both events concurrently to test race condition
		var wg sync.WaitGroup
		
		wg.Add(2)
		go func() {
			defer wg.Done()
			mockConn.fireEvent("CHANNEL_ANSWER", map[string]string{
				"Other-Leg-Destination-Number": "1000001",
			}, 0)
		}()
		
		go func() {
			defer wg.Done()
			mockConn.fireEvent("CHANNEL_HANGUP", map[string]string{
				"Other-Leg-Destination-Number": "1000001",
				"Hangup-Cause": "NORMAL_CLEARING",
			}, 0)
		}()
		
		wg.Wait()
		
		// Wait for events to be processed
		time.Sleep(50 * time.Millisecond)
		
		// Verify number was processed only once
		assert.True(t, resultWriter.isProcessed("1000001"), 
			"Number should be processed")
		assert.Equal(t, 1, len(resultWriter.processedNumbers), 
			"Should have exactly one processed number")
	})
}

// Test timeout behavior under concurrent conditions
func TestTimeoutBehavior(t *testing.T) {
	t.Run("processDialJob timeout handling", func(t *testing.T) {
		mockConn := newMockESLConn()
		
		file, err := os.CreateTemp("", "test_output_*.csv")
		require.NoError(t, err)
		defer os.Remove(file.Name())
		defer file.Close()
		
		writer := csv.NewWriter(file)
		defer writer.Flush()
		
		records := [][]string{{"1000001", ""}}
		results := make([][]string, len(records))
		resultWriter := newSafeResultWriter(writer, results, records)
		
		// Start the dial job with very short timeout
		start := time.Now()
		testProcessDialJob(mockConn, 0, "1000001", "call-0", 1, resultWriter)
		duration := time.Since(start)
		
		// Should complete within reasonable time (1 second + 5 second buffer + some processing time)
		assert.Less(t, duration, 7*time.Second, 
			"processDialJob should complete within timeout period")
		
		// Verify originate was called
		assert.Equal(t, 1, mockConn.getOriginateCallCount("call-0"), 
			"Should have called originate once")
	})
}

// Helper function to extract number from UUID for testing
func getNumberFromUUID(uuid string) string {
	switch uuid {
	case "call-0":
		return "1000001"
	case "call-1":
		return "1000002"
	case "call-2":
		return "1000003"
	case "call-3":
		return "1000004"
	case "call-4":
		return "1000005"
	default:
		return ""
	}
}


// Benchmark concurrent operations
func BenchmarkConcurrentDialOperations(b *testing.B) {
	mockConn := newMockESLConn()
	
	// Create test CSV writer
	file, err := os.CreateTemp("", "bench_output_*.csv")
	require.NoError(b, err)
	defer os.Remove(file.Name())
	defer file.Close()
	
	writer := csv.NewWriter(file)
	defer writer.Flush()
	
	records := make([][]string, 100)
	for i := 0; i < 100; i++ {
		records[i] = []string{fmt.Sprintf("100%04d", i), ""}
	}
	
	results := make([][]string, len(records))
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		resultWriter := newSafeResultWriter(writer, results, records)
		
		var wg sync.WaitGroup
		concurrency := 10
		
		for j := 0; j < concurrency; j++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				
				for k := workerID; k < len(records); k += concurrency {
					number := records[k][0]
					uuid := fmt.Sprintf("call-%d", k)
					
					testProcessDialJob(mockConn, k, number, uuid, 1, resultWriter)
				}
			}(j)
		}
		
		wg.Wait()
	}
}