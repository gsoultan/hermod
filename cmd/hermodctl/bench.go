package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	benchDuration int
	benchPayload  string
)

var benchCmd = &cobra.Command{
	Use:   "bench [workflow-id]",
	Short: "Benchmark a workflow's performance",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workflowID := args[0]
		url := fmt.Sprintf("%s/api/workflows/test", viper.GetString("url"))

		fmt.Printf("Benchmarking workflow %s for %d seconds...\n", workflowID, benchDuration)

		var payload map[string]interface{}
		if benchPayload != "" {
			json.Unmarshal([]byte(benchPayload), &payload)
		} else {
			payload = map[string]interface{}{"test": "data", "timestamp": time.Now().Unix()}
		}

		reqBody := map[string]interface{}{
			"workflow_id": workflowID,
			"message":     payload,
		}
		body, _ := json.Marshal(reqBody)

		start := time.Now()
		count := 0
		errors := 0
		var totalLat time.Duration

		timeout := time.After(time.Duration(benchDuration) * time.Second)

	loop:
		for {
			select {
			case <-timeout:
				break loop
			default:
				reqStart := time.Now()
				req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
				req.Header.Set("Content-Type", "application/json")
				if key := viper.GetString("key"); key != "" {
					req.Header.Set("Authorization", "Bearer "+key)
				}

				resp, err := http.DefaultClient.Do(req)
				lat := time.Since(reqStart)

				if err != nil || resp.StatusCode != http.StatusOK {
					errors++
				} else {
					count++
					totalLat += lat
				}
				if resp != nil {
					resp.Body.Close()
				}
			}
		}

		elapsed := time.Since(start)
		fmt.Printf("\nBenchmark Results:\n")
		fmt.Printf("  Total Requests: %d\n", count+errors)
		fmt.Printf("  Successful:     %d\n", count)
		fmt.Printf("  Failed:         %d\n", errors)
		fmt.Printf("  Duration:       %v\n", elapsed)
		fmt.Printf("  Throughput:     %.2f req/s\n", float64(count)/elapsed.Seconds())
		if count > 0 {
			fmt.Printf("  Avg Latency:    %v\n", totalLat/time.Duration(count))
		}
	},
}

func init() {
	benchCmd.Flags().IntVarP(&benchDuration, "duration", "d", 10, "Duration of benchmark in seconds")
	benchCmd.Flags().StringVarP(&benchPayload, "payload", "p", "", "JSON payload to send")
	rootCmd.AddCommand(benchCmd)
}
