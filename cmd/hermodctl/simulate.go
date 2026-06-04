package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() {
	rootCmd.AddCommand(simulateCmd)
}

var simulateCmd = &cobra.Command{
	Use:   "simulate [workflow-file] [data-file]",
	Short: "Simulate a workflow locally with data from a file",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		wfFile := args[0]
		dataFile := args[1]

		fmt.Printf("🚀 Simulating workflow from %s with data from %s\n", wfFile, dataFile)

		wfData, err := os.ReadFile(wfFile)
		if err != nil {
			fmt.Printf("Error reading workflow file: %v\n", err)
			return
		}

		msgData, err := os.ReadFile(dataFile)
		if err != nil {
			fmt.Printf("Error reading data file: %v\n", err)
			return
		}

		// In a real CLI, we might run the engine locally.
		// For this implementation, we'll use the server's simulation API if available,
		// or provide a placeholder for local execution.

		client := &http.Client{Timeout: 30 * time.Second}
		url := fmt.Sprintf("%s/api/workflows/simulate", viper.GetString("url"))

		payload := map[string]any{
			"workflow": string(wfData),
			"data":     string(msgData),
		}
		jsonPayload, _ := json.Marshal(payload)

		req, _ := http.NewRequest("POST", url, bytes.NewReader(jsonPayload))
		req.Header.Set("Content-Type", "application/json")
		if key := viper.GetString("key"); key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}

		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("Error connecting to API: %v\n", err)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			fmt.Printf("❌ Simulation failed (%s): %s\n", resp.Status, string(body))
			return
		}

		fmt.Printf("✅ Simulation finished. Results:\n%s\n", string(body))
	},
}
